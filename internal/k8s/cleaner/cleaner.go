package cleaner

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Cleaner removes runtime fields from K8s resources so they can be safely
// stored in GitLab and re-applied to a cluster.
//
// The cleaner aims for **round-trip stability**: Clean(K8sLive) and Clean(GitLabYAML)
// should produce identical objects when they describe the same logical resource.
// Achieving that requires stripping fields that the API server defaults — those
// are present on K8s-live objects but absent (or different) in user-authored YAML.
type Cleaner interface {
	Clean(obj *unstructured.Unstructured) *unstructured.Unstructured
}

type cleaner struct{}

func NewCleaner() Cleaner {
	return &cleaner{}
}

// Clean returns a deep copy of obj with runtime / server-populated fields removed.
func (c *cleaner) Clean(obj *unstructured.Unstructured) *unstructured.Unstructured {
	out := obj.DeepCopy()
	data := out.Object

	// Remove top-level status — never round-trippable.
	delete(data, "status")

	// Clean metadata.
	if metadata, ok := data["metadata"].(map[string]interface{}); ok {
		for _, k := range []string{
			"managedFields", "resourceVersion", "uid", "creationTimestamp",
			"generation", "selfLink", "deletionTimestamp", "deletionGracePeriodSeconds",
			"ownerReferences", "finalizers",
		} {
			delete(metadata, k)
		}

		if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
			for key := range annotations {
				if shouldRemoveAnnotation(key) {
					delete(annotations, key)
				}
			}
			if len(annotations) == 0 {
				delete(metadata, "annotations")
			}
		}
		if labels, ok := metadata["labels"].(map[string]interface{}); ok {
			if len(labels) == 0 {
				delete(metadata, "labels")
			}
		}
	}

	cleanByKind(out)
	return out
}

// cleanByKind removes fields that are auto-populated by the API server for
// specific resource kinds.
func cleanByKind(obj *unstructured.Unstructured) {
	switch obj.GetKind() {
	case "Service":
		for _, f := range []string{
			"clusterIP", "clusterIPs",
			"ipFamilies", "ipFamilyPolicy",
			"internalTrafficPolicy", "externalTrafficPolicy",
			"sessionAffinity", "sessionAffinityConfig",
			"publishNotReadyAddresses",
			"allocateLoadBalancerNodePorts",
			"loadBalancerIP", "loadBalancerSourceRanges", "loadBalancerClass",
			"healthCheckNodePort",
		} {
			unstructured.RemoveNestedField(obj.Object, "spec", f)
		}
		if ports, found, _ := unstructured.NestedSlice(obj.Object, "spec", "ports"); found {
			for _, p := range ports {
				if pm, ok := p.(map[string]interface{}); ok {
					delete(pm, "nodePort")
					// `protocol: TCP` and `targetPort: <same as port>` are defaulted
					// by the API server. Drop only when they match defaults.
					if v, ok := pm["protocol"]; ok && v == "TCP" {
						delete(pm, "protocol")
					}
				}
			}
			_ = unstructured.SetNestedSlice(obj.Object, ports, "spec", "ports")
		}

	case "ServiceAccount":
		unstructured.RemoveNestedField(obj.Object, "secrets")
		unstructured.RemoveNestedField(obj.Object, "imagePullSecrets")

	case "Pod":
		unstructured.RemoveNestedField(obj.Object, "spec", "nodeName")
		unstructured.RemoveNestedField(obj.Object, "spec", "serviceAccount")
		dropDefaultedPodSpec(obj.Object, "spec")

	case "PersistentVolumeClaim":
		unstructured.RemoveNestedField(obj.Object, "spec", "volumeName")

	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		dropDefaultPodTemplate(obj)
		dropDeploymentDefaults(obj)

	case "Job":
		dropDefaultPodTemplate(obj)
		dropJobControllerLabels(obj)

	case "CronJob":
		// CronJob has its own template path: spec.jobTemplate.spec.template
		dropDefaultedPodSpec(obj.Object, "spec", "jobTemplate", "spec", "template", "spec")
		dropDefaultedPodTemplateMetadata(obj.Object, "spec", "jobTemplate", "spec", "template", "metadata")

	case "Endpoints", "EndpointSlice":
		// Fully runtime — should not be synced.
	}
}

// dropDeploymentDefaults strips top-level fields that the API server defaults.
// e.g. revisionHistoryLimit: 10, progressDeadlineSeconds: 600,
// strategy.type: RollingUpdate (with default rollingUpdate maxSurge/maxUnavailable).
func dropDeploymentDefaults(obj *unstructured.Unstructured) {
	specPath := []string{"spec"}

	// `revisionHistoryLimit: 10` is the server default for Deployment/StatefulSet/DaemonSet.
	if v, found, _ := unstructured.NestedInt64(obj.Object, append(specPath, "revisionHistoryLimit")...); found && v == 10 {
		unstructured.RemoveNestedField(obj.Object, append(specPath, "revisionHistoryLimit")...)
	}
	// `progressDeadlineSeconds: 600` is the Deployment default.
	if v, found, _ := unstructured.NestedInt64(obj.Object, append(specPath, "progressDeadlineSeconds")...); found && v == 600 {
		unstructured.RemoveNestedField(obj.Object, append(specPath, "progressDeadlineSeconds")...)
	}

	// strategy.rollingUpdate gets defaulted with maxSurge: 25%, maxUnavailable: 25%.
	if strat, found, _ := unstructured.NestedMap(obj.Object, append(specPath, "strategy")...); found {
		if t, _ := strat["type"].(string); t == "RollingUpdate" {
			if ru, ok := strat["rollingUpdate"].(map[string]interface{}); ok {
				maxSurge, _ := ru["maxSurge"]
				maxUnavail, _ := ru["maxUnavailable"]
				if (maxSurge == "25%" || maxSurge == nil) && (maxUnavail == "25%" || maxUnavail == nil) {
					delete(strat, "rollingUpdate")
				}
			}
			// `strategy.type: RollingUpdate` is the default; drop if it's the only key left.
			if len(strat) == 1 {
				unstructured.RemoveNestedField(obj.Object, append(specPath, "strategy")...)
			} else {
				_ = unstructured.SetNestedMap(obj.Object, strat, append(specPath, "strategy")...)
			}
		}
	}
}

// dropJobControllerLabels strips the controller-uid label injected by the Job controller.
func dropJobControllerLabels(obj *unstructured.Unstructured) {
	for _, path := range [][]string{
		{"spec", "selector", "matchLabels"},
		{"spec", "template", "metadata", "labels"},
	} {
		if labels, found, _ := unstructured.NestedMap(obj.Object, path...); found {
			delete(labels, "controller-uid")
			delete(labels, "batch.kubernetes.io/controller-uid")
			delete(labels, "job-name")
			delete(labels, "batch.kubernetes.io/job-name")
			_ = unstructured.SetNestedMap(obj.Object, labels, path...)
		}
	}
}

// dropDefaultPodTemplate cleans the pod template of a workload (spec.template).
func dropDefaultPodTemplate(obj *unstructured.Unstructured) {
	dropDefaultedPodSpec(obj.Object, "spec", "template", "spec")
	dropDefaultedPodTemplateMetadata(obj.Object, "spec", "template", "metadata")
}

// dropDefaultedPodTemplateMetadata removes server-set noise from the pod template metadata.
func dropDefaultedPodTemplateMetadata(obj map[string]interface{}, path ...string) {
	meta, found, _ := unstructured.NestedMap(obj, path...)
	if !found {
		return
	}
	// creationTimestamp: null is what client-go marshalers emit for embedded ObjectMeta.
	delete(meta, "creationTimestamp")
	if len(meta) == 0 {
		unstructured.RemoveNestedField(obj, path...)
	} else {
		_ = unstructured.SetNestedMap(obj, meta, path...)
	}
}

// dropDefaultedPodSpec strips fields the API server defaults on a PodSpec.
// path is the JSON path to the pod spec (e.g. "spec.template.spec" for a Deployment).
func dropDefaultedPodSpec(obj map[string]interface{}, path ...string) {
	spec, found, _ := unstructured.NestedMap(obj, path...)
	if !found {
		return
	}

	// Server-injected SA admission fields.
	delete(spec, "serviceAccount") // duplicate of serviceAccountName

	// Top-level pod spec defaults.
	dropIfEqual(spec, "restartPolicy", "Always")
	dropIfEqualInt(spec, "terminationGracePeriodSeconds", 30)
	dropIfEqualInt(spec, "deprecatedServiceAccount", -1) // never present, just guard
	dropIfEqual(spec, "dnsPolicy", "ClusterFirst")
	dropIfEqual(spec, "schedulerName", "default-scheduler")
	dropIfEqual(spec, "preemptionPolicy", "PreemptLowerPriority")
	dropIfEqualInt(spec, "priority", 0)
	// `securityContext: {}` is what the server emits when none is set.
	if sc, ok := spec["securityContext"].(map[string]interface{}); ok && len(sc) == 0 {
		delete(spec, "securityContext")
	}

	// Strip auto-mounted SA token volumes.
	if vols, ok := spec["volumes"].([]interface{}); ok {
		filtered := vols[:0]
		for _, v := range vols {
			if vm, ok := v.(map[string]interface{}); ok {
				if name, _ := vm["name"].(string); isAutoSAVolume(name) {
					continue
				}
			}
			filtered = append(filtered, v)
		}
		if len(filtered) == 0 {
			delete(spec, "volumes")
		} else {
			spec["volumes"] = filtered
		}
	}

	// Clean each container.
	if cs, ok := spec["containers"].([]interface{}); ok {
		for _, c := range cs {
			if cm, ok := c.(map[string]interface{}); ok {
				cleanContainer(cm)
			}
		}
	}
	if cs, ok := spec["initContainers"].([]interface{}); ok {
		for _, c := range cs {
			if cm, ok := c.(map[string]interface{}); ok {
				cleanContainer(cm)
			}
		}
	}

	_ = unstructured.SetNestedMap(obj, spec, path...)
}

// cleanContainer strips defaults that the API server fills on a Container spec.
func cleanContainer(c map[string]interface{}) {
	dropIfEqual(c, "imagePullPolicy", "IfNotPresent") // default for non-:latest tags
	dropIfEqual(c, "terminationMessagePath", "/dev/termination-log")
	dropIfEqual(c, "terminationMessagePolicy", "File")
	if r, ok := c["resources"].(map[string]interface{}); ok && len(r) == 0 {
		delete(c, "resources")
	}

	// Strip auto-mounted SA token volumeMounts.
	if mounts, ok := c["volumeMounts"].([]interface{}); ok {
		filtered := mounts[:0]
		for _, m := range mounts {
			if mm, ok := m.(map[string]interface{}); ok {
				if name, _ := mm["name"].(string); isAutoSAVolume(name) {
					continue
				}
			}
			filtered = append(filtered, m)
		}
		if len(filtered) == 0 {
			delete(c, "volumeMounts")
		} else {
			c["volumeMounts"] = filtered
		}
	}

	// Container ports: protocol defaults to TCP.
	if ports, ok := c["ports"].([]interface{}); ok {
		for _, p := range ports {
			if pm, ok := p.(map[string]interface{}); ok {
				dropIfEqual(pm, "protocol", "TCP")
			}
		}
	}

	// Probe defaults — these are auto-filled when a probe is configured but
	// many fields are left blank by the user.
	for _, probeKey := range []string{"livenessProbe", "readinessProbe", "startupProbe"} {
		if pr, ok := c[probeKey].(map[string]interface{}); ok {
			dropIfEqualInt(pr, "successThreshold", 1)
			dropIfEqualInt(pr, "failureThreshold", 3)
			dropIfEqualInt(pr, "periodSeconds", 10)
			dropIfEqualInt(pr, "timeoutSeconds", 1)
			// Probe handler defaults (e.g. httpGet.scheme: HTTP)
			if hg, ok := pr["httpGet"].(map[string]interface{}); ok {
				dropIfEqual(hg, "scheme", "HTTP")
			}
			if tcp, ok := pr["tcpSocket"].(map[string]interface{}); ok {
				_ = tcp // no defaults to strip currently
			}
		}
	}

	// Lifecycle hook handler defaults (httpGet.scheme: HTTP).
	if lc, ok := c["lifecycle"].(map[string]interface{}); ok {
		for _, hookKey := range []string{"postStart", "preStop"} {
			if h, ok := lc[hookKey].(map[string]interface{}); ok {
				if hg, ok := h["httpGet"].(map[string]interface{}); ok {
					dropIfEqual(hg, "scheme", "HTTP")
				}
			}
		}
	}
}

func isAutoSAVolume(name string) bool {
	return strings.HasPrefix(name, "kube-api-access-") || strings.HasPrefix(name, "default-token-")
}

// dropIfEqual deletes the key if its value matches the given default string.
func dropIfEqual(m map[string]interface{}, key string, defaultVal string) {
	if v, ok := m[key]; ok {
		if s, _ := v.(string); s == defaultVal {
			delete(m, key)
		}
	}
}

// dropIfEqualInt deletes the key if its value matches the given default int.
// Handles the various numeric types JSON/YAML decoders may produce.
func dropIfEqualInt(m map[string]interface{}, key string, defaultVal int64) {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case int:
			if int64(n) == defaultVal {
				delete(m, key)
			}
		case int64:
			if n == defaultVal {
				delete(m, key)
			}
		case float64:
			if int64(n) == defaultVal {
				delete(m, key)
			}
		}
	}
}

func shouldRemoveAnnotation(key string) bool {
	if key == "kubectl.kubernetes.io/last-applied-configuration" ||
		key == "deployment.kubernetes.io/revision" ||
		key == "control-plane.alpha.kubernetes.io/leader" {
		return true
	}
	prefixes := []string{
		"kubernetes.io/",
		"k8s.io/",
		"control-plane.alpha.kubernetes.io/",
		"endpoints.kubernetes.io/",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
