package socialcontent

import (
	"fmt"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

func TestPinSpoolFilterAndExternalSortReplay(t *testing.T) {
	spool, err := newPinSpool()
	if err != nil {
		t.Fatalf("newPinSpool: %v", err)
	}
	defer spool.Close()

	const total = 45000
	pins := make([]BackfillPin, 0, total)
	for i := 0; i < total; i++ {
		pins = append(pins, backfillPin(
			fmt.Sprintf("pin-%05d:i0", i),
			OperationCreate,
			int64(total-i),
			fmt.Sprintf(`{"text":"%d"}`, i),
		))
	}
	if err := spool.appendPins("simplebuzz", pins); err != nil {
		t.Fatalf("appendPins: %v", err)
	}

	if err := spool.filterPins("simplebuzz", "simplebuzz.selected", func(pin BackfillPin) (bool, error) {
		return pin.Timestamp%2 == 0, nil
	}); err != nil {
		t.Fatalf("filterPins: %v", err)
	}

	var replayed []int64
	replay := func(pin *aggregator.PinInscription) error {
		replayed = append(replayed, pin.Timestamp)
		return nil
	}
	if err := spool.sortAndReplay("simplebuzz.selected", replay); err != nil {
		t.Fatalf("sortAndReplay: %v", err)
	}
	if len(replayed) != total/2 {
		t.Fatalf("replayed = %d, want %d", len(replayed), total/2)
	}
	for index := 1; index < len(replayed); index++ {
		if replayed[index] <= replayed[index-1] {
			t.Fatalf("replay not sorted at %d: %d before %d", index, replayed[index-1], replayed[index])
		}
	}
	if replayed[0] != 2 || replayed[len(replayed)-1] != total {
		t.Fatalf("unexpected first/last replay timestamps: %d ... %d", replayed[0], replayed[len(replayed)-1])
	}
}
