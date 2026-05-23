package cleaner

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Cleaner removes runtime fields from K8s resources so they can be safely
// stored in GitLab and re-applied to a cluster.
type Cleaner interface {
	Clean(obj *unstructured.Unstructured) *unstructured.Unstructured
}

type cleaner struct{}

func NewCleaner() Cleaner {
	return &cleaner{}
}

// Clean returns a deep copy of obj with runtime / server-populated fields removed.
// The returned object is safe to write to GitLab and apply back to a cluster.
func (c *cleaner) Clean(obj *unstructured.Unstructured) *unstructured.Unstructured {
	out := obj.DeepCopy()
	data := out.Object

	// Remove top-level status — never round-trippable.
	delete(data, "status")

	// Clean metadata.
	if metadata, ok := data["metadata"].(map[string]interface{}); ok {
		// Common server-managed metadata fields.
		for _, k := range []string{
			"managedFields", "resourceVersion", "uid", "creationTimestamp",
			"generation", "selfLink", "deletionTimestamp", "deletionGracePeriodSeconds",
			"ownerReferences", "finalizers",
		} {
			delete(metadata, k)
		}

		// Clean annotations.
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

		// Drop empty labels / annotations.
		if labels, ok := metadata["labels"].(map[string]interface{}); ok {
			if len(labels) == 0 {
				delete(metadata, "labels")
			}
		}
	}

	// Per-Kind cleanup of immutable / server-injected fields.
	cleanByKind(out)

	return out
}

// cleanByKind removes fields that are auto-populated by the API server for
// specific resource kinds — these would otherwise either round-trip noise to
// GitLab or cause Apply to fail with "field is immutable".
func cleanByKind(obj *unstructured.Unstructured) {
	switch obj.GetKind() {
	case "Service":
		// Drop server-injected / immutable / cluster-specific fields so the YAML
		// is portable to other clusters.
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
		// nodePort gets auto-assigned for NodePort/LoadBalancer; keeping the
		// stored value would prevent re-applying onto another cluster, so drop.
		if ports, found, _ := unstructured.NestedSlice(obj.Object, "spec", "ports"); found {
			for _, p := range ports {
				if pm, ok := p.(map[string]interface{}); ok {
					delete(pm, "nodePort")
				}
			}
			_ = unstructured.SetNestedSlice(obj.Object, ports, "spec", "ports")
		}

	case "ServiceAccount":
		// `secrets` is auto-populated with the auto-mounted token reference.
		unstructured.RemoveNestedField(obj.Object, "secrets")
		unstructured.RemoveNestedField(obj.Object, "imagePullSecrets")

	case "Pod":
		// nodeName is set by the scheduler; serviceAccount duplicates serviceAccountName.
		unstructured.RemoveNestedField(obj.Object, "spec", "nodeName")
		unstructured.RemoveNestedField(obj.Object, "spec", "serviceAccount")

	case "PersistentVolumeClaim":
		// volumeName is bound at runtime.
		unstructured.RemoveNestedField(obj.Object, "spec", "volumeName")

	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		// strategy / revisionHistoryLimit / progressDeadlineSeconds get defaulted.
		// Keep them only if explicitly set is hard to detect, so we leave them.
		// Drop the pod template's auto-defaulted scheduler / service-account fields
		// when they match the standard defaults.
		dropDefaultPodTemplate(obj)

	case "Job", "CronJob":
		// Same template clean-up.
		dropDefaultPodTemplate(obj)
		// Plus the controller-uid label set by the Job controller.
		if labels, found, _ := unstructured.NestedMap(obj.Object, "spec", "selector", "matchLabels"); found {
			delete(labels, "controller-uid")
			delete(labels, "batch.kubernetes.io/controller-uid")
			_ = unstructured.SetNestedMap(obj.Object, labels, "spec", "selector", "matchLabels")
		}
		if labels, found, _ := unstructured.NestedMap(obj.Object, "spec", "template", "metadata", "labels"); found {
			delete(labels, "controller-uid")
			delete(labels, "batch.kubernetes.io/controller-uid")
			_ = unstructured.SetNestedMap(obj.Object, labels, "spec", "template", "metadata", "labels")
		}

	case "Endpoints", "EndpointSlice":
		// Fully runtime — should not be synced.
	}
}

// dropDefaultPodTemplate removes fields injected by the API server / scheduler
// from a workload's pod template (spec.template.spec).
func dropDefaultPodTemplate(obj *unstructured.Unstructured) {
	podSpecPath := []string{"spec", "template", "spec"}
	if _, found, _ := unstructured.NestedMap(obj.Object, podSpecPath...); !found {
		return
	}
	// Auto-injected by service account admission.
	unstructured.RemoveNestedField(obj.Object, append(podSpecPath, "serviceAccount")...) // duplicate of serviceAccountName
	unstructured.RemoveNestedField(obj.Object, append(podSpecPath, "nodeName")...)

	// Strip default-mounted volumes / volumeMounts that the SA admission controller adds:
	//   - kube-api-access-* projected volume
	//   - default-token-* (legacy)
	if vols, found, _ := unstructured.NestedSlice(obj.Object, append(podSpecPath, "volumes")...); found {
		filtered := vols[:0]
		for _, v := range vols {
			if vm, ok := v.(map[string]interface{}); ok {
				if name, _ := vm["name"].(string); strings.HasPrefix(name, "kube-api-access-") || strings.HasPrefix(name, "default-token-") {
					continue
				}
			}
			filtered = append(filtered, v)
		}
		_ = unstructured.SetNestedSlice(obj.Object, filtered, append(podSpecPath, "volumes")...)
	}
	if containers, found, _ := unstructured.NestedSlice(obj.Object, append(podSpecPath, "containers")...); found {
		for _, c := range containers {
			if cm, ok := c.(map[string]interface{}); ok {
				if mounts, ok := cm["volumeMounts"].([]interface{}); ok {
					filtered := mounts[:0]
					for _, m := range mounts {
						if mm, ok := m.(map[string]interface{}); ok {
							if name, _ := mm["name"].(string); strings.HasPrefix(name, "kube-api-access-") || strings.HasPrefix(name, "default-token-") {
								continue
							}
						}
						filtered = append(filtered, m)
					}
					cm["volumeMounts"] = filtered
				}
			}
		}
		_ = unstructured.SetNestedSlice(obj.Object, containers, append(podSpecPath, "containers")...)
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
