package ibcli

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// Keep the original comparison paths as an ordering oracle and benchmark
// baseline. Fixtures include equal IPs, numeric TTLs, text and Unicode names.
func referenceDefaultRecordCompare(left, right TypedRecord) int {
	leftIP, leftOK := firstSortIPAddress(recordName(left.Item, left.Type))
	rightIP, rightOK := firstSortIPAddress(recordName(right.Item, right.Type))
	switch {
	case leftOK && rightOK:
		if result := leftIP.Compare(rightIP); result != 0 {
			return result
		}
	case leftOK:
		return -1
	case rightOK:
		return 1
	}
	return compareCaseInsensitiveText(defaultRecordSortKey(left), defaultRecordSortKey(right))
}

func referenceSortRecords(records []TypedRecord) {
	sort.Slice(records, func(i, j int) bool {
		return referenceDefaultRecordCompare(records[i], records[j]) < 0
	})
}

func referenceApplyRecordSort(records []TypedRecord, option RecordSort) {
	sort.SliceStable(records, func(i, j int) bool {
		left, right := records[i], records[j]
		if option.Field == "ttl" {
			return applySortDirection(compareRecordTTL(recordTTL(left.Item), recordTTL(right.Item)), option.Desc) < 0
		}
		leftValue, rightValue := recordSortValue(left, option.Field), recordSortValue(right, option.Field)
		if option.Field == "name" || option.Field == "value" {
			return compareRecordIPAwareText(leftValue, rightValue, option.Desc) < 0
		}
		return applySortDirection(compareCaseInsensitiveText(leftValue, rightValue), option.Desc) < 0
	})
}

func recordSortFixtures(count int) []TypedRecord {
	records := make([]TypedRecord, count)
	for i := range records {
		item := map[string]any{
			"_ref": fmt.Sprintf("record:a/%d", i), "name": fmt.Sprintf("App-%05d.example.com", i),
			"zone": "example.com", "address": fmt.Sprintf("192.0.%d.%d", (i/256)%256, i%256),
			"ttl":     []any{nil, "", 300, "60", "default", "9999999999999999999999999"}[i%6],
			"comment": []string{"", "Production", "production", "éclair", "测试"}[i%5],
		}
		recordType := "a"
		switch i % 9 {
		case 0:
			recordType = "ptr"
			item["zone"] = "192.0.0.0/16"
			item["ptrdname"] = item["name"]
		case 1:
			recordType = "host"
			delete(item, "address")
			item["ipv4addrs"] = []map[string]any{{"ipv4addr": "192.0.2.1"}, {"ipv4addr": "192.0.2.10"}}
		case 2:
			recordType = "mx"
			delete(item, "address")
			item["preference"], item["mail_exchanger"] = 10, "mail.example.com"
		case 3:
			item["name"] = "Éclair.example.com"
		case 4:
			item["address"] = "2001:db8::1"
		case 5:
			delete(item, "address")
			item["record"] = map[string]any{"name": item["name"], "ipv4addr": "192.0.2.15"}
		case 6:
			item["address"] = "2001:0db8::1"
		case 7:
			recordType = "txt"
			delete(item, "address")
			item["text"] = "long text with punctuation, Unicode 界 and whitespace"
		}
		records[i] = TypedRecord{Type: recordType, Item: item}
	}
	rand.New(rand.NewSource(1)).Shuffle(len(records), func(i, j int) { records[i], records[j] = records[j], records[i] })
	return records
}

func recordSortIDs(records []TypedRecord) []string {
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.Item["_ref"].(string)
	}
	return ids
}

func TestPrecomputedRecordSortMatchesOriginalOrdering(t *testing.T) {
	want := recordSortFixtures(500)
	got := recordSortFixtures(500)
	referenceSortRecords(want)
	sortRecords(got)
	if !reflect.DeepEqual(recordSortIDs(got), recordSortIDs(want)) {
		t.Fatal("default ordering differs from original comparator")
	}
	for _, field := range recordSortFields {
		for _, desc := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/descending=%v", field, desc), func(t *testing.T) {
				want := append([]TypedRecord(nil), got...)
				actual := append([]TypedRecord(nil), got...)
				option := RecordSort{Enabled: true, Field: field, Desc: desc}
				referenceApplyRecordSort(want, option)
				applyRecordSort(actual, option)
				if !reflect.DeepEqual(recordSortIDs(actual), recordSortIDs(want)) {
					t.Fatal("selected-field ordering or stable ties differ from original comparator")
				}
			})
		}
	}
}

func TestPrecomputedRecordSortRebuildsKeysAfterMutation(t *testing.T) {
	records := []TypedRecord{
		{Type: "a", Item: map[string]any{"name": "a.example.com", "address": "192.0.2.1"}},
		{Type: "a", Item: map[string]any{"name": "b.example.com", "address": "192.0.2.2"}},
	}
	sortRecords(records)
	records[0].Item["name"] = "z.example.com"
	sortRecords(records)
	assertSortedRecordNames(t, records, []string{"b.example.com", "z.example.com"})
	records[0].Item["address"] = "192.0.2.100"
	applyRecordSort(records, RecordSort{Enabled: true, Field: "value"})
	assertSortedRecordNames(t, records, []string{"z.example.com", "b.example.com"})
}

func TestRecordSortNormalizesNestedFieldsBeforeCapturingKeys(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%v", reverse), func(t *testing.T) {
			records := make([]TypedRecord, 0, 3)
			for i, name := range []string{"m.example.com", "z.example.com", "a.example.com"} {
				zone := "example.com"
				recordType := "a"
				address := fmt.Sprintf("192.0.2.%d", i+1)
				if reverse {
					zone, recordType = "192.0.2.0/24", "ptr"
					name = []string{"10", "100", "2"}[i]
					address = "192.0.2." + name
				}
				records = append(records, TypedRecord{Type: recordType, Item: map[string]any{
					"_ref":   fmt.Sprintf("record:%s/%d", recordType, i),
					"record": map[string]any{"name": name, "zone": zone, "ipv4addr": address},
				}})
			}
			want := []string{"a.example.com", "m.example.com", "z.example.com"}
			if reverse {
				want = []string{"192.0.2.2", "192.0.2.10", "192.0.2.100"}
			}
			sortRecords(records)
			assertSortedRecordNames(t, records, want)
			sortRecords(records)
			assertSortedRecordNames(t, records, want)
		})
	}
	// Selected-field keys must also see fields promoted from nested records.
	records := []TypedRecord{
		{Type: "a", Item: map[string]any{"record": map[string]any{"name": "high", "ttl": 300}}},
		{Type: "a", Item: map[string]any{"record": map[string]any{"name": "low", "ttl": 60}}},
	}
	applyRecordSort(records, RecordSort{Enabled: true, Field: "ttl"})
	assertSortedRecordNames(t, records, []string{"low", "high"})
}

func BenchmarkSortRecords10k(b *testing.B) {
	fixtures := recordSortFixtures(10000)
	for _, record := range fixtures {
		// Normalize nested fields before timing either implementation; value
		// extraction can populate the outer map in the existing record format.
		_ = recordValue(record.Type, record.Item)
	}
	for _, test := range []struct {
		name string
		sort func([]TypedRecord)
	}{{"original", referenceSortRecords}, {"precomputed", sortRecords}} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			records := make([]TypedRecord, len(fixtures))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(records, fixtures)
				test.sort(records)
			}
		})
	}
}

func BenchmarkApplyRecordSort10k(b *testing.B) {
	fixtures := recordSortFixtures(10000)
	for _, record := range fixtures {
		_ = recordValue(record.Type, record.Item)
	}
	for _, field := range []string{"name", "value", "ttl"} {
		for _, original := range []bool{true, false} {
			b.Run(fmt.Sprintf("%s/original=%v", field, original), func(b *testing.B) {
				b.ReportAllocs()
				records := make([]TypedRecord, len(fixtures))
				option := RecordSort{Enabled: true, Field: field}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					copy(records, fixtures)
					if original {
						referenceApplyRecordSort(records, option)
					} else {
						applyRecordSort(records, option)
					}
				}
			})
		}
	}
}
