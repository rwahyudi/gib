# Include NS Record Type Filter Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Allow `ib dns list --type ns` and `ib dns search --type ns` to filter/list Infoblox nameserver records.

**Architecture:** Add `ns` to the supported DNS record type registry used by flag parsing and completion, then validate it against existing allrecords decoding/output behavior. The list/search paths already filter decoded `TypedRecord.Type` values through `parseRecordTypes`, so the smallest safe change is to make `ns` an accepted type and add regression tests proving both list and search filters retain NS records and exclude other records.

**Tech Stack:** Go, Cobra CLI, existing `internal/ibcli` DNS record helpers, Go unit tests with `go test`.

---

## Current context / assumptions

- Repository: `/home/rwahyudi/gib`.
- Relevant code:
  - `internal/ibcli/dns.go:76-121` defines `recordTypes`, currently including `a`, `aaaa`, `cname`, `txt`, `mx`, `srv`, `host`, and `ptr`, but not `ns`.
  - `internal/ibcli/commands.go:655-719` wires `ib dns list` and parses `--type` via `parseRecordTypes(typeFilter)`.
  - `internal/ibcli/commands.go:722-782` wires `ib dns search` and parses `--type` via `parseRecordTypes(typeFilter)`.
  - `internal/ibcli/dns.go:2763-2783` applies search type filtering against `TypedRecord.Type`.
  - `internal/ibcli/dns.go:3132-3154` applies list type filtering against `TypedRecord.Type`.
  - `internal/ibcli/dns_test.go:496-505` already verifies unsupported allrecords NS refs decode to `Type == "ns"`.
  - `internal/ibcli/dns_test.go:518-533` verifies NS ref display suppresses raw refs.
- Assumption: The desired behavior is to accept `--type ns` for list/search filters and shell completion. This does not require adding create/edit/delete support for NS records.
- User preference: after implementation changes, rebuild `/home/rwahyudi/bin/ib` with `env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -buildvcs=false -o /home/rwahyudi/bin/ib ./cmd/ib`.

---

## Proposed approach

1. Add an `ns` entry to `recordTypes` in `internal/ibcli/dns.go` so `parseRecordTypes`, `supportedRecordTypes`, and flag completion all recognize nameserver records.
2. Keep the implementation read/filter-only. Do not add DNS write support unless a failing test proves it is required.
3. Add tests that prove:
   - `parseRecordTypes("ns")` accepts the type.
   - `filterListedRecords(..., SearchOptions{Types: []string{"ns"}})` keeps only NS records.
   - `collectSearchResults(... SearchOptions{Types: []string{"ns"}})` returns decoded NS records from allrecords and excludes A/HOST records.
4. Run targeted tests first, then build the user-facing binary.
5. Check docs/help examples only if the CLI help or README enumerates supported `--type` values explicitly.

---

## Files likely to change

- Modify: `internal/ibcli/dns.go:76-121`
- Modify: `internal/ibcli/dns_test.go` near existing NS tests at `internal/ibcli/dns_test.go:496-533`, and/or near list/search filter tests around `internal/ibcli/dns_test.go:1450-1590`
- Possibly modify docs only if an explicit supported-types list exists:
  - `README.md`
  - `packaging/man/ib.1`

---

### Task 1: Add failing parser test for `--type ns`

**Objective:** Prove `ns` is accepted as a valid record type by the same parser used by `ib dns list --type` and `ib dns search --type`.

**Files:**
- Modify: `internal/ibcli/dns_test.go`
- Test target: `go test ./internal/ibcli -run TestParseRecordTypesAcceptsNS -count=1`

**Step 1: Write failing test**

Add this test near other DNS type/helper tests, for example after `TestUnsupportedAllRecordsTypeDecodesNSRef`:

```go
func TestParseRecordTypesAcceptsNS(t *testing.T) {
	types, err := parseRecordTypes("ns")
	if err != nil {
		t.Fatalf("parse ns record type: %v", err)
	}
	if len(types) != 1 || types[0] != "ns" {
		t.Fatalf("types = %#v, want []string{\"ns\"}", types)
	}
}
```

**Step 2: Run test to verify failure**

Run:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/ibcli -run TestParseRecordTypesAcceptsNS -count=1
```

Expected: FAIL with an error similar to `unsupported record type "ns"`.

**Step 3: Do not implement yet**

Leave implementation for Task 2 so the red/green transition is clear.

**Step 4: Commit**

Do not commit a failing test by itself unless working in a TDD-only checkpoint branch. Prefer committing after Task 2 passes.

---

### Task 2: Add `ns` to supported record types

**Objective:** Make `--type ns` accepted by parsing and completion without changing write behavior.

**Files:**
- Modify: `internal/ibcli/dns.go:76-121`
- Test: `internal/ibcli/dns_test.go`

**Step 1: Add minimal implementation**

In `internal/ibcli/dns.go`, add this entry to `recordTypes` near other forward record types, for example after `mx` and before `srv`:

```go
	"ns": {
		Object:            "record:ns",
		SearchValueFields: []string{"nameserver"},
		ReturnFields:      "name,nameserver,ttl,use_ttl,view,zone,comment",
	},
```

Notes:
- `ValueField` can remain empty because there is no current create/edit support planned for NS records.
- If tests reveal the Infoblox allrecords response uses `nsdname` instead of `nameserver`, add that field to `SearchValueFields` and `ReturnFields` only after verifying neighboring code expects it.

**Step 2: Run parser test to verify pass**

Run:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/ibcli -run TestParseRecordTypesAcceptsNS -count=1
```

Expected: PASS.

**Step 3: Run nearby existing NS tests**

Run:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/ibcli -run 'TestUnsupportedAllRecordsTypeDecodesNSRef|TestRecordDisplaySuppressesRefStrings|TestParseRecordTypesAcceptsNS' -count=1
```

Expected: PASS.

**Step 4: Commit after Task 3 tests are added**

Wait to commit until the behavior-level list/search tests pass.

---

### Task 3: Add list filter regression test for NS records

**Objective:** Prove the `ib dns list --type ns` filtering path keeps NS records and excludes other record types.

**Files:**
- Modify: `internal/ibcli/dns_test.go`
- Test target: `go test ./internal/ibcli -run TestFilterListedRecordsIncludesNS -count=1`

**Step 1: Write failing/pass-through test**

Add this test near `TestRecordDisplaySuppressesRefStrings` or near other list output/filter tests:

```go
func TestFilterListedRecordsIncludesNS(t *testing.T) {
	records := []TypedRecord{
		{Type: "ns", Item: map[string]any{"name": "example.com", "nameserver": "ns1.example.com", "zone": "example.com"}},
		{Type: "a", Item: map[string]any{"name": "app.example.com", "ipv4addr": "192.0.2.10", "zone": "example.com"}},
	}

	filtered := filterListedRecords(records, SearchOptions{Types: []string{"ns"}})
	if len(filtered) != 1 {
		t.Fatalf("filtered records = %d, want 1: %#v", len(filtered), filtered)
	}
	if filtered[0].Type != "ns" {
		t.Fatalf("filtered type = %q, want ns", filtered[0].Type)
	}
}
```

**Step 2: Run test**

Run:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/ibcli -run TestFilterListedRecordsIncludesNS -count=1
```

Expected after Task 2: PASS. If it fails, fix only the filtering/type normalization needed for `ns`.

**Step 3: Commit still waits for Task 4**

Keep changes together until both list and search behavior are covered.

---

### Task 4: Add search filter regression test for NS records

**Objective:** Prove `ib dns search <keyword> --type ns` returns decoded NS records from allrecords and excludes non-NS records.

**Files:**
- Modify: `internal/ibcli/dns_test.go`
- Test target: `go test ./internal/ibcli -run TestSearchTypeFilterIncludesNS -count=1`

**Step 1: Write test using a local WAPI server**

Add this test near existing search-scope tests around `TestSearchRecursiveIncludesChildZones`:

```go
func TestSearchTypeFilterIncludesNS(t *testing.T) {
	encodedNSRef := base64.RawURLEncoding.EncodeToString([]byte("dns.bind_ns$example.com"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/zone_auth"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{"fqdn": "example.com", "view": "default", "zone_format": "FORWARD", "primary_type": "Grid"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/allrecords"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{
					{
						"_ref":       "allrecords/" + encodedNSRef + ":example.com/default",
						"type":       "UNSUPPORTED",
						"name":       "example.com",
						"nameserver": "ns1.example.com",
						"zone":       "example.com",
					},
					{
						"type":    "HOST_IPV4ADDR",
						"name":    "app.example.com",
						"address": "192.0.2.10",
						"zone":    "example.com",
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := testApp(t)
	records, err := app.collectSearchResults(
		Profile{DNSView: "default", DefaultZone: "example.com"},
		testWapiClient(server),
		SearchOptions{Keyword: "example.com", Types: []string{"ns"}},
	)
	if err != nil {
		t.Fatalf("collect search results: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %#v", len(records), records)
	}
	if records[0].Type != "ns" {
		t.Fatalf("record type = %q, want ns", records[0].Type)
	}
}
```

**Step 2: Run test**

Run:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/ibcli -run TestSearchTypeFilterIncludesNS -count=1
```

Expected after Task 2: PASS.

**Step 3: If the test fails because the NS value does not match search keywords**

Inspect `recordValue` and `searchValuesMatch` behavior. Add the smallest helper support needed for NS display/search value, for example making `recordValue("ns", item)` prefer `nameserver` if it does not already. Then rerun the test.

---

### Task 5: Run focused DNS tests and format Go files

**Objective:** Verify the changed DNS filter behavior and keep Go formatting clean.

**Files:**
- Modify: `internal/ibcli/dns.go`
- Modify: `internal/ibcli/dns_test.go`

**Step 1: Format edited Go files**

Run:

```bash
gofmt -w internal/ibcli/dns.go internal/ibcli/dns_test.go
```

Expected: command exits 0 with no output.

**Step 2: Run focused tests**

Run:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/ibcli -run 'TestParseRecordTypesAcceptsNS|TestFilterListedRecordsIncludesNS|TestSearchTypeFilterIncludesNS|TestUnsupportedAllRecordsTypeDecodesNSRef|TestRecordDisplaySuppressesRefStrings' -count=1
```

Expected: PASS.

**Step 3: Run broader DNS package tests only if focused tests pass**

Run:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/ibcli -run 'Test.*DNS|Test.*Record|Test.*Search|TestParseRecord' -count=1
```

Expected: PASS. If this regex is too broad/slow, fall back to `go test ./internal/ibcli -count=1`.

---

### Task 6: Check documentation/help impact

**Objective:** Ensure docs do not contradict the new `--type ns` support.

**Files:**
- Inspect: `README.md`
- Inspect: `packaging/man/ib.1`
- Modify only if an explicit supported record type list omits `ns`.

**Step 1: Search docs for explicit type lists**

Run read-only checks:

```bash
python3 - <<'PY'
from pathlib import Path
for path in [Path('README.md'), Path('NOTES.md'), Path('packaging/man/ib.1')]:
    if not path.exists():
        continue
    text = path.read_text(errors='ignore')
    for i, line in enumerate(text.splitlines(), 1):
        lower = line.lower()
        if '--type' in lower or 'record type' in lower or ' a,' in lower or 'aaaa' in lower:
            print(f'{path}:{i}: {line}')
PY
```

Expected: Identify whether any docs enumerate record types.

**Step 2: Update docs only if needed**

If docs list supported types without `ns`, update the line to include `ns`. Do not add speculative documentation if no explicit list exists.

**Step 3: Run docs-neutral check**

No docs-specific linter is required unless the repository already has one. If docs changed, include them in the final commit.

---

### Task 7: Build binary to user-facing path

**Objective:** Satisfy the user's standing preference to rebuild `/home/rwahyudi/bin/ib` after every code change.

**Files:**
- Build output: `/home/rwahyudi/bin/ib`

**Step 1: Build binary**

Run:

```bash
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -buildvcs=false -o /home/rwahyudi/bin/ib ./cmd/ib
```

Expected: command exits 0 with no output.

**Step 2: Verify command accepts `--type ns` in help/completion-adjacent path**

Run:

```bash
/home/rwahyudi/bin/ib dns list --help
/home/rwahyudi/bin/ib dns search --help
```

Expected: both help outputs still show `--type, -t` as a record type filter. Help may not enumerate values; that is okay.

---

### Task 8: Commit implementation

**Objective:** Commit only the files changed for this NS filter task.

**Files:**
- `internal/ibcli/dns.go`
- `internal/ibcli/dns_test.go`
- Documentation files only if Task 6 changed them

**Step 1: Check working tree**

Run:

```bash
git status --short
```

Expected: only intended files are modified.

**Step 2: Review diff**

Run:

```bash
git diff -- internal/ibcli/dns.go internal/ibcli/dns_test.go README.md NOTES.md packaging/man/ib.1
```

Expected: diff only adds NS support/tests and any necessary docs correction.

**Step 3: Commit**

Run:

```bash
git add internal/ibcli/dns.go internal/ibcli/dns_test.go
# If docs changed, include them too:
# git add README.md NOTES.md packaging/man/ib.1
git commit -m "Support NS record type filters"
```

Expected: commit succeeds and prints a new commit hash.

---

## Tests / validation summary

Minimum validation before final response:

```bash
gofmt -w internal/ibcli/dns.go internal/ibcli/dns_test.go
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/ibcli -run 'TestParseRecordTypesAcceptsNS|TestFilterListedRecordsIncludesNS|TestSearchTypeFilterIncludesNS|TestUnsupportedAllRecordsTypeDecodesNSRef|TestRecordDisplaySuppressesRefStrings' -count=1
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go test ./internal/ibcli -count=1
env GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go-mod go build -buildvcs=false -o /home/rwahyudi/bin/ib ./cmd/ib
git status --short
```

Expected:
- Focused tests pass.
- `./internal/ibcli` package tests pass.
- Binary rebuild exits 0.
- Worktree is clean after commit.

---

## Risks, tradeoffs, and open questions

- Risk: Adding `ns` to `recordTypes` may make create/edit/delete validation accept `ns` unintentionally if those flows rely only on the same map. Before committing, check `internal/ibcli/commands.go:848` and DNS write paths. If write commands become enabled for NS without full payload support, extract a separate read/filter supported-types set or add a `Writable` field to `RecordSpec` and keep NS read-only.
- Risk: Infoblox allrecords may expose nameserver values under a field other than `nameserver`. Existing tests should use the field shape already handled by display/search helpers. If uncertain, inspect `recordValue` and neighboring allrecords normalization before choosing fields.
- Tradeoff: This plan avoids direct live Infoblox calls, keeping tests deterministic.
- Open question: Should `ib dns create ns ...` be supported later? Not part of this task unless the user explicitly asks.
- Open question: Should help text enumerate supported record types? Current help says “record type filter, comma-separated”; if values are not enumerated today, keep that unchanged.

---

## Acceptance criteria

- `parseRecordTypes("ns")` succeeds.
- `supportedRecordTypes()` and `--type` completion include `ns`.
- `filterListedRecords(... Types: []string{"ns"})` keeps NS records and excludes other types.
- `collectSearchResults(... Types: []string{"ns"})` keeps decoded NS allrecords and excludes other types.
- Existing NS decode/display tests still pass.
- `/home/rwahyudi/bin/ib` is rebuilt after implementation.
- Changes are committed with a concise imperative message.
