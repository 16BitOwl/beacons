package registry_test

import (
	"github.com/16bitowl/beacons/internal/model"
)

// newRecord builds a minimal valid Record for use in store tests.
func newRecord(sourceID, id, upstream string) model.Record {
	return model.Record{
		ID:         id,
		SourceID:   sourceID,
		SourceName: "test-source",
		Upstream:   upstream,
		Type:       model.RecordTypeA,
		Name:       id + ".example.com",
		Value:      "1.2.3.4",
	}
}
