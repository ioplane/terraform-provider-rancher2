#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ${1:-} == inside ]]; then
  mkdir -p /cache/{build,modules,tools,gotmp,evidence,home,lint}
  export PATH="/cache/tools:$PATH" GOBIN=/cache/tools GOTMPDIR=/cache/gotmp
  export GOTOOLCHAIN=local GOENV=off GOSUMDB=sum.golang.org
  export GOCACHE=/cache/build GOMODCACHE=/cache/modules CGO_ENABLED=1
  export HOME=/cache/home GOLANGCI_LINT_CACHE=/cache/lint
  export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0=/src
  cd /cache
  go version
  [[ $(go env GOVERSION) == go1.27.2 ]] || { echo '::error::Containerfile compiler differs from the reviewed Go 1.27.2'; exit 1; }
  go env GOPATH GOTOOLCHAIN GOSUMDB CGO_ENABLED GOEXPERIMENT
  sha256sum /src/Containerfile.dev /src/.golangci.yml /src/go.mod /src/go.sum
  export GOFLAGS='-buildvcs=false'
  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
  go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
  go install github.com/zricethezav/gitleaks/v8@v8.30.1
  for spec in 'golangci-lint github.com/golangci/golangci-lint/v2 v2.15.0' 'govulncheck golang.org/x/vuln v1.9.0' 'gitleaks github.com/zricethezav/gitleaks/v8 v8.30.1'; do
    read -r tool module version <<< "$spec"
    go version -m "/cache/tools/$tool" | tee "/cache/evidence/$tool-buildinfo.txt"
    grep -F $'\tmod\t'"$module"$'\t'"$version"$'\t' "/cache/evidence/$tool-buildinfo.txt"
  done
  golangci-lint version
  govulncheck -version
  # Go-installed gitleaks may print a placeholder version; buildinfo above is authoritative.
  gitleaks version
  cat > /cache/check.go <<'GO'
package main

import (
    "encoding/json"
    "fmt"
    "io"
    "os"
    "strings"
)

func check() error {
    f, err := os.Open(os.Args[2])
    if err != nil { return fmt.Errorf("open evidence: %w", err) }
    defer f.Close()
    d := json.NewDecoder(f)
    if os.Args[1] == "vulnerability-input" {
        configs, sboms := 0, 0
        for {
            var event struct {
                Config *struct { Level string `json:"scan_level"`; Mode string `json:"scan_mode"`; Scanner string `json:"scanner_version"` } `json:"config"`
                SBOM *struct { Roots []string `json:"roots"`; Modules []json.RawMessage `json:"modules"` } `json:"SBOM"`
            }
            if err := d.Decode(&event); err == io.EOF { break } else if err != nil { return fmt.Errorf("decode scanner JSON: %w", err) }
            if event.Config != nil {
                configs++
                if event.Config.Level != "symbol" || event.Config.Mode != "source" || event.Config.Scanner != "v1.9.0" { return fmt.Errorf("unexpected scanner configuration") }
            }
            if event.SBOM != nil {
                sboms++
                if len(event.SBOM.Roots) == 0 || len(event.SBOM.Modules) == 0 { return fmt.Errorf("scanner received no root/module input") }
                fmt.Printf("scanner input: roots=%d modules=%d\n", len(event.SBOM.Roots), len(event.SBOM.Modules))
            }
        }
        if configs != 1 || sboms != 1 { return fmt.Errorf("incomplete scanner stream: config=%d SBOM=%d", configs, sboms) }
        return nil
    }
    if os.Args[1] == "secrets" {
        var findings []json.RawMessage
        if err := d.Decode(&findings); err != nil { return fmt.Errorf("decode secret findings: %w", err) }
        fmt.Printf("secret findings: %d\n", len(findings))
        if len(findings) != 0 { return fmt.Errorf("secret scan rejected %d findings", len(findings)) }
        return nil
    }
    prefix := "github.com/rancher/terraform-provider-rancher2/"
    required := strings.Fields(`internal/ranchercatalog:TestOriginalRancherCatalogContract
rancher2:TestCatalogAndHostnameOriginalContract
rancher2:TestFlattenAppV2
rancher2:TestExpandChartInstallV2
rancher2:TestExpandChartUpgradeV2
rancher2:TestExpandChartInstallActionV2
rancher2:TestExpandChartUpgradeActionV2
rancher2:TestWaitForMachineConfigV2TimeoutPreservesIdentity
rancher2:TestUpdateSecretV2ConflictRefreshesResourceVersion
rancher2:TestSecretV2JSONWire
rancher2:TestSDKOwnerStateAndID
rancher2:TestSDKOwnerSecretForceNew
rancher2:TestSDKOwnerComputedSet
rancher2:TestFlattenSecretV2
rancher2:TestExpandSecretV2
rancher2:TestBootstrapReadPreservesIdentity
rancher2:TestFeatureUpdateRecoversAfterReadinessFailures
rancher2:TestFeatureUpdateRecoversAfterReadinessFailures/update_502_recovers
rancher2:TestFeatureUpdateRecoversAfterReadinessFailures/update_503_recovers
rancher2:TestFeatureUpdateRecoversAfterReadinessFailures/update_403_fails
rancher2:TestGetRegistryByFiltersValidation
rancher2:TestGetRegistryByFiltersValidation/nil_filters
rancher2:TestGetRegistryByFiltersValidation/missing_name
rancher2:TestGetRegistryByFiltersValidation/non_string_name
rancher2:TestGetRegistryByFiltersValidation/missing_project_id
rancher2:TestGetRegistryByFiltersValidation/non_string_project_id
rancher2:TestGetRegistryByFiltersValidation/empty_name
rancher2:TestGetRegistryByFiltersValidation/empty_project_id
rancher2:TestGetRegistryByFiltersValidation/project_list
rancher2:TestGetRegistryByFiltersValidation/namespaced_list
rancher2:TestProviderRejectsInvalidCACerts
rancher2:TestProviderRejectsInvalidCACerts/invalid_text
rancher2:TestProviderRejectsInvalidCACerts/invalid_certificate
rancher2:TestProviderRejectsInvalidCACerts/whitespace
rancher2:TestProviderRejectsInvalidCACerts/invalid_insecure
rancher2:TestProviderRejectsInvalidCACerts/empty_ca
rancher2:TestProviderRejectsInvalidCACerts/valid_ca
rancher2:TestProviderRejectsInvalidCACerts/valid_bundle_with_unrelated_data
rancher2:TestProviderRejectsInvalidCACerts/empty_ca_insecure
rancher2:TestRancherReadinessDeadline
rancher2:TestRancherReadinessDeadline/headers
rancher2:TestRancherReadinessDeadline/body
rancher2:TestRancherReadinessDeadline/zero_first_probe
rancher2:TestRancherReadinessDeadline/negative_first_probe
rancher2:TestRancherReadinessDeadline/pong
rancher2:TestRancherReadinessDeadline/unexpected_reply`)
    runs, passes, packages := map[string]int{}, map[string]int{}, map[string]int{}
    total, top := 0, 0
    for {
        var event struct { Action, Package, Test string }
        if err := d.Decode(&event); err == io.EOF { break } else if err != nil { return fmt.Errorf("decode test JSON: %w", err) }
        if event.Action == "fail" || event.Action == "skip" { return fmt.Errorf("rejected %s: %s %s", event.Action, event.Package, event.Test) }
        if event.Test == "" { if event.Action == "pass" { packages[event.Package]++ }; continue }
        key := event.Package + ":" + event.Test
        if event.Action == "run" { runs[key]++; total++; if !strings.Contains(event.Test, "/") { top++ } }
        if event.Action == "pass" { passes[key]++ }
    }
    fmt.Printf("critical test input: %d top-level, %d total run events, %d required names\n", top, total, len(required))
    if total < 57 { return fmt.Errorf("critical test input shrank: %d < 57", total) }
    for key, count := range runs { if count != 1 || passes[key] != 1 { return fmt.Errorf("test did not run/pass exactly once: %s run=%d pass=%d", key, count, passes[key]) } }
    for key := range passes { if runs[key] != 1 { return fmt.Errorf("pass without run: %s", key) } }
    for _, key := range required { if runs[prefix+key] != 1 || passes[prefix+key] != 1 { return fmt.Errorf("required test missing: %s", key) } }
    for _, pkg := range []string{"internal/ranchercatalog", "rancher2"} { if packages[prefix+pkg] != 1 { return fmt.Errorf("package did not pass: %s", pkg) } }
    fmt.Printf("critical tests: %d run / %d pass / 0 fail / 0 skip; required %d names present\n", total, len(passes), len(required))
    return nil
}

func main() {
    if err := check(); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}
GO
  go build -trimpath -o /cache/check /cache/check.go
  printf '[extend]\nuseDefault = true\n' > /cache/gitleaks.toml
  export GOFLAGS='-mod=readonly -buildvcs=false'
  cd /src
  failed=0
  gate() {
    local label=$1 status
    local -a statuses
    shift
    echo "::group::$label"
    set +e
    "$@" 2>&1 | tee "/cache/evidence/$label.log"
    statuses=("${PIPESTATUS[@]}")
    status=${statuses[0]}
    if (( statuses[1] != 0 )); then status=${statuses[1]}; fi
    set -e
    printf '%s exit_code=%d\n' "$label" "$status"
    echo '::endgroup::'
    if (( status != 0 )); then failed=1; fi
  }
  scan_secrets() {
    local source=$1
    shift
    if [[ -e "$source/.gitleaksignore" || -L "$source/.gitleaksignore" ]]; then
      echo '::error::source .gitleaksignore is forbidden (including symlinks)'
      return 1
    fi
    gitleaks "$@" "$source"
  }
  echo 'Scope: root product module only; test/ cloud acceptance requires a separate credentialed gate.'
  gate module-metadata bash -euo pipefail -c '
    mkdir /cache/tidy
    git archive HEAD | tar -x -C /cache/tidy
    cd /cache/tidy
    GOFLAGS=-buildvcs=false go mod tidy
    cmp /src/go.mod go.mod
    cmp /src/go.sum go.sum
    sha256sum go.mod go.sum > /cache/evidence/tidy-first.sha256
    GOFLAGS=-buildvcs=false go mod tidy
    sha256sum -c /cache/evidence/tidy-first.sha256
    go mod verify
  '
  gate build go build -trimpath -o /cache/provider .
  gate build-packages go build -trimpath ./...
  gate vet go vet ./...
  gate format golangci-lint fmt --diff ./...
  gate lint golangci-lint run --timeout=15m ./...
  selection='^(TestOriginalRancherCatalogContract|TestCatalogAndHostnameOriginalContract|TestFlattenAppV2|TestExpandChart(Install|Upgrade)(Action)?V2|TestWaitForMachineConfigV2TimeoutPreservesIdentity|TestUpdateSecretV2ConflictRefreshesResourceVersion|TestSecretV2JSONWire|TestSDKOwner.*|TestFlattenSecretV2|TestExpandSecretV2|TestBootstrapReadPreservesIdentity|TestFeatureUpdateRecoversAfterReadinessFailures|TestGetRegistryByFiltersValidation|TestProviderRejectsInvalidCACerts|TestRancherReadinessDeadline)$'
  gate critical-tests go test -json -race -shuffle=on -covermode=atomic -count=1 -run "$selection" ./internal/ranchercatalog ./rancher2
  gate critical-input /cache/check tests /cache/evidence/critical-tests.log
  gate vulnerability-json govulncheck -db=https://vuln.go.dev -scan=symbol -json ./...
  gate vulnerability-input /cache/check vulnerability-input /cache/evidence/vulnerability-json.log
  gate vulnerabilities bash -euo pipefail -c 'govulncheck -mode=convert -show=verbose < /cache/evidence/vulnerability-json.log'
  if [[ $SCAN_BASE == 0000000000000000000000000000000000000000 && $CI_EVENT == push ]]; then
    echo 'Secret input: first push, complete committed source snapshot (not an empty revision range).'
    mkdir /cache/secret-snapshot
    git archive HEAD | tar -x -C /cache/secret-snapshot
    count=$(git ls-files | wc -l)
    (( count > 0 )) || { echo '::error::secret snapshot has no tracked input'; exit 1; }
    echo "secret snapshot tracked paths: $count"
    gate secrets scan_secrets /cache/secret-snapshot dir --config=/cache/gitleaks.toml --gitleaks-ignore-path=/dev/null --ignore-gitleaks-allow --redact=100 --no-banner --report-format=json --report-path=/cache/evidence/secrets.json
  else
    [[ $SCAN_BASE =~ ^[0-9a-f]{40}$ && $SCAN_BASE != 0000000000000000000000000000000000000000 ]] || { echo '::error::invalid secret scan base'; exit 1; }
    git cat-file -e "$SCAN_BASE^{commit}"
    count=$(git rev-list --count "$SCAN_BASE..$EXPECTED_HEAD")
    (( count > 0 )) || { echo '::error::secret scan revision range has no commits'; exit 1; }
    echo "Secret input: $SCAN_BASE..$EXPECTED_HEAD, commits=$count; native filename-aware added-line scan."
    gate secrets scan_secrets /src git --log-opts="--full-history --diff-merges=first-parent $SCAN_BASE..$EXPECTED_HEAD" --config=/cache/gitleaks.toml --gitleaks-ignore-path=/dev/null --ignore-gitleaks-allow --redact=100 --no-banner --report-format=json --report-path=/cache/evidence/secrets.json
  fi
  if [[ -f /cache/evidence/secrets.json ]]; then cat /cache/evidence/secrets.json; fi
  gate secret-input /cache/check secrets /cache/evidence/secrets.json
  du -sk /cache || failed=1
  df -Pk /cache || failed=1
  for counter in memory.max memory.peak memory.events; do
    if [[ -r /sys/fs/cgroup/$counter ]]; then
      printf '%s: ' "$counter"
      cat "/sys/fs/cgroup/$counter" || failed=1
    fi
  done
  exit "$failed"
fi

[[ ${EXPECTED_HEAD:-} =~ ^[0-9a-f]{40}$ ]] || { echo '::error::invalid expected head'; exit 1; }
[[ $(git rev-parse HEAD) == "$EXPECTED_HEAD" ]] || { echo '::error::checkout is not the requested source head'; exit 1; }
printf 'source_head=%s\n' "$EXPECTED_HEAD"
[[ $(find .github/workflows -type f \( -name '*.yml' -o -name '*.yaml' \) | sort) == .github/workflows/fork-blacksmith.yml ]] || { echo '::error::inherited workflow YAML has not been quarantined'; exit 1; }
[[ -f Containerfile.dev && -f internal/ranchercatalog/types_test.go && -f rancher2/root_contract_test.go ]] || { echo '::error::bootstrap incomplete: product Containerfile.dev or committed contract tests absent'; exit 1; }
command -v podman >/dev/null || { echo '::error::rootless Podman is required on the Blacksmith runner'; exit 1; }
[[ $(id -u) != 0 && $(podman info --format '{{.Host.Security.Rootless}}') == true ]] || { echo '::error::rootful Podman is forbidden; no sudo or socket fallback'; exit 1; }
podman version
work=$(mktemp -d "${RUNNER_TEMP:-/tmp}/fork-check.XXXXXXXX")
image_id=''
cleanup() {
  local status=$? cleanup_status=0 cid
  trap - EXIT
  if [[ -s $work/cid ]]; then
    cid=$(cat "$work/cid")
    if podman container exists "$cid"; then
      podman inspect "$cid" --format 'container_exit={{.State.ExitCode}} oom_killed={{.State.OOMKilled}}' || cleanup_status=1
      podman rm --force "$cid" || cleanup_status=1
    fi
  fi
  if [[ -n $image_id ]]; then podman image rm --no-prune "$image_id" || cleanup_status=1; fi
  if [[ -d $work/cache && ! -L $work/cache ]]; then
    du -sk "$work/cache" || cleanup_status=1
    df -Pk "$work/cache" || cleanup_status=1
    find -P "$work/cache" -type d -exec chmod u+w -- {} + || cleanup_status=1
  fi
  # Only this invocation's mktemp directory is removed on the ephemeral runner.
  rm -rf -- "$work" || cleanup_status=1
  if (( status == 0 && cleanup_status != 0 )); then status=$cleanup_status; fi
  exit "$status"
}
trap cleanup EXIT
mkdir "$work/cache"
df -Pk "$work/cache"
printf '{"auths":{}}\n' > "$work/auth.json"
podman build --authfile "$work/auth.json" --http-proxy=false --cpu-period=100000 --cpu-quota=400000 \
  --memory=12g --memory-swap=12g --ulimit=nproc=512:512 --cap-drop=all --security-opt=no-new-privileges \
  --build-arg "SOURCE_REVISION=$EXPECTED_HEAD" --build-arg "BUILD_CREATED=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --label "fork.check.owner=${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}" \
  --iidfile "$work/image-id" --file Containerfile.dev .
image_id=$(cat "$work/image-id")
[[ $image_id =~ ^sha256:[0-9a-f]{64}$ ]] || { echo '::error::unexpected built image ID'; exit 1; }
podman image inspect "$image_id" --format '{{.Id}} {{json .Config.Labels}}'
podman run --cidfile "$work/cid" --authfile "$work/auth.json" --http-proxy=false \
  --read-only --read-only-tmpfs=false --cap-drop=all --security-opt=no-new-privileges \
  --userns=keep-id --user "$(id -u):$(id -g)" --cpus=4 --memory=12g --memory-swap=12g --pids-limit=512 \
  --volume "$work/cache:/cache:rw" --tmpfs /tmp:rw,nosuid,nodev,size=512m,mode=1777 \
  --volume "$PWD:/src:ro" --workdir /src \
  --env "EXPECTED_HEAD=$EXPECTED_HEAD" --env "SCAN_BASE=$SCAN_BASE" --env "CI_EVENT=$CI_EVENT" \
  --env GOMAXPROCS=4 --env GOTMPDIR=/cache/gotmp --env GOPATH=/cache/gopath \
  "$image_id" bash /src/.github/scripts/fork-check.sh inside
