$ErrorActionPreference = 'Continue'
$env:PATH = "C:\Program Files\Git\bin;" + $env:PATH

git config --global user.email "1105836858@qq.com"
git config --global user.name "kiro"
git config --global init.defaultBranch main

Write-Host "=== git config ==="
git config --global user.email
git config --global user.name

Write-Host "`n=== status ==="
git status --short | Select-Object -First 5
Write-Host "(...)"

Write-Host "`n=== adding files ==="
git add -A

Write-Host "`n=== files staged (count) ==="
$count = (git diff --cached --name-only | Measure-Object).Count
Write-Host "Total files: $count"

Write-Host "`n=== checking for sensitive files in staging ==="
git diff --cached --name-only | Select-String -Pattern "\.env|secret|token|credentials|kubeconfig|auth\.json|users\.json|encryption\.key" | Select-Object -First 10

Write-Host "`n=== commit ==="
git commit -m "Initial commit: YAML Sync - GitLab and K8s bidirectional sync with MFA, RBAC, approval workflow"

Write-Host "`n=== remote ==="
git remote remove origin 2>$null
git remote add origin https://github.com/wdx0810/sync-yaml.git
git remote -v

Write-Host "`n=== branch ==="
git branch -M main
git log --oneline -1

Write-Host "`n=== DONE - ready to push ==="
Write-Host "Run: git push -u origin main"
