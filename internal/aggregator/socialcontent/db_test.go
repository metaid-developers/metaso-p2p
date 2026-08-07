package socialcontent

import "testing"

func TestInvertedTimestampOrdersNewestFirst(t *testing.T) {
	cases := []struct {
		older int64
		newer int64
	}{
		{1782653223, 1782707008},
		{1782707008, 1786062975},
		{1782653223, 1786062975},
		{100, 200},
	}
	for _, c := range cases {
		olderKey := invertedTimestamp(c.older)
		newerKey := invertedTimestamp(c.newer)
		if newerKey >= olderKey {
			t.Fatalf("invertedTimestamp(%d)=%q must sort before invertedTimestamp(%d)=%q", c.newer, newerKey, c.older, olderKey)
		}
		if decoded, ok := decodeInvertedTimestamp(newerKey); !ok || decoded != c.newer {
			t.Fatalf("decodeInvertedTimestamp(%q) = %d,%v want %d", newerKey, decoded, ok, c.newer)
		}
	}
}
