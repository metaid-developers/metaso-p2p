package storage

import "testing"

// TestScanPrefixDescOrder pins the descending scan primitive: it visits
// exactly the reverse of the ascending prefix scan, and an fn error stops it.
func TestScanPrefixDescOrder(t *testing.T) {
	store := NewPebbleStore(t.TempDir())
	defer store.Close()

	for i := 0; i < 5; i++ {
		if err := store.Set("ns", []byte{'a', byte('0' + i)}, []byte{byte(i)}); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	var asc, desc []string
	if err := store.ScanPrefix("ns", []byte("a"), func(key, value []byte) error {
		asc = append(asc, string(key))
		return nil
	}); err != nil {
		t.Fatalf("asc scan: %v", err)
	}
	if err := store.ScanPrefixDesc("ns", []byte("a"), func(key, value []byte) error {
		desc = append(desc, string(key))
		return nil
	}); err != nil {
		t.Fatalf("desc scan: %v", err)
	}
	if len(asc) != 5 || len(desc) != 5 {
		t.Fatalf("scan lengths: asc=%d desc=%d, want 5/5", len(asc), len(desc))
	}
	for i := range asc {
		if asc[i] != desc[len(desc)-1-i] {
			t.Fatalf("desc not the reverse of asc: asc=%v desc=%v", asc, desc)
		}
	}

	// An fn error stops the walk and is propagated, same contract as ScanPrefix.
	visited := 0
	if err := store.ScanPrefixDesc("ns", []byte("a"), func(key, value []byte) error {
		visited++
		return errTestStop
	}); err != errTestStop {
		t.Fatalf("desc scan error = %v, want the fn error back", err)
	}
	if visited != 1 {
		t.Fatalf("stop error did not halt iteration: visited=%d", visited)
	}
}

type testStopError struct{}

func (testStopError) Error() string { return "stop" }

var errTestStop error = testStopError{}
