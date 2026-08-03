package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// Named for the test, not `secret`: this is package scope in `model`, shared
// with every other file in the package.
const testSecret = "c3VwZXItc2VjcmV0LWtleS1tYXRlcmlhbA=="

func testBooth() Booth {
	eventID := 7
	return Booth{
		ID:       1,
		EventID:  &eventID,
		Name:     "ชมรมเปตอง",
		Category: "sports",
		Secret:   testSecret,
	}
}

// The rule this whole file exists for: whoever holds a booth's secret can mint
// valid check-in codes for it for the rest of the event.
func TestPublicBoothCarriesNoSecret(t *testing.T) {
	encoded, err := json.Marshal(NewPublicBooth(testBooth()))
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	body := string(encoded)
	if strings.Contains(body, testSecret) {
		t.Errorf("the secret reached the response body: %s", body)
	}
	if strings.Contains(body, "secret") {
		t.Errorf("a secret key appeared in the response: %s", body)
	}
}

func TestPublicBoothKeepsTheFieldsTheClientNeeds(t *testing.T) {
	var decoded map[string]any
	encoded, _ := json.Marshal(NewPublicBooth(testBooth()))
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	for _, key := range []string{"id", "event_id", "name", "category"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing %q in %v", key, decoded)
		}
	}
}

// A booth not yet assigned to an event is a real state, and the iOS client
// decodes `event_id` as optional.
func TestPublicBoothAllowsANilEventID(t *testing.T) {
	booth := testBooth()
	booth.EventID = nil

	encoded, _ := json.Marshal(NewPublicBooth(booth))
	if !strings.Contains(string(encoded), `"event_id":null`) {
		t.Errorf("expected a null event_id, got %s", encoded)
	}
}

// Even the internal type must not spill it, for the day someone marshals a
// Booth directly instead of converting first.
func TestBoothItselfDoesNotMarshalItsSecret(t *testing.T) {
	encoded, _ := json.Marshal(testBooth())

	if strings.Contains(string(encoded), testSecret) {
		t.Errorf("the internal type leaked its secret: %s", encoded)
	}
}

// Go marshals a nil slice as `null`, and the iOS client reads `null` for a list
// as a broken response — the bug /events already shipped with.
func TestAnEmptyBoothSliceMarshalsAsAnArray(t *testing.T) {
	encoded, _ := json.Marshal(make([]PublicBooth, 0))

	if string(encoded) != "[]" {
		t.Errorf("expected [], got %s", encoded)
	}
}
