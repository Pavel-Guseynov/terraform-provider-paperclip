package fixtures

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSkillSyncSchemaCases(t *testing.T) {
	data, err := os.ReadFile("skill-sync-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name    string
		Request json.RawMessage
		Valid   bool
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			_, err := SkillSyncSnapshot(bytes.NewReader(tc.Request))
			if (err == nil) != tc.Valid {
				t.Fatalf("valid=%v, error=%v", tc.Valid, err)
			}
		})
	}
}

func TestSkillSyncRejectsMalformedJSON(t *testing.T) {
	if _, err := SkillSyncSnapshot(strings.NewReader(`{`)); err == nil {
		t.Fatal("expected malformed JSON rejection")
	}
}

func TestSkillSyncNormalizesBothSnapshotProjections(t *testing.T) {
	snapshot, err := SkillSyncSnapshot(strings.NewReader(`{"mode":"replace","desiredSkills":[" paperclip ",{"key":" pinned ","versionId":"11111111-1111-4111-8111-111111111111"},{"key":"null-pin","versionId":null}]}`))
	want := map[string]interface{}{
		"adapterType": "antigravity_local", "supported": true, "mode": "replace",
		"desiredSkills": []string{"paperclipai/paperclip/paperclip", "paperclip", "pinned", "null-pin"},
		"desiredSkillEntries": []map[string]interface{}{
			{"key": "paperclipai/paperclip/paperclip", "versionId": nil},
			{"key": "paperclip", "versionId": nil},
			{"key": "pinned", "versionId": "11111111-1111-4111-8111-111111111111"},
			{"key": "null-pin", "versionId": nil},
		},
	}
	if err != nil || !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("snapshot=%#v, error=%v; want %#v", snapshot, err, want)
	}
}

func TestSkillSyncRetainsOnlyRequiredSkillForEmptyOrExplicitRequiredSelections(t *testing.T) {
	for _, desired := range []string{`[]`, `[" PAPERCLIPAI/PAPERCLIP/PAPERCLIP "]`, `[{"key":"paperclipai/paperclip/paperclip","versionId":null},"paperclipai/paperclip/paperclip"]`} {
		t.Run(desired, func(t *testing.T) {
			snapshot, err := SkillSyncSnapshot(strings.NewReader(`{"mode":"replace","desiredSkills":` + desired + `}`))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(snapshot["desiredSkills"], []string{"paperclipai/paperclip/paperclip"}) ||
				!reflect.DeepEqual(snapshot["desiredSkillEntries"], []map[string]interface{}{{"key": "paperclipai/paperclip/paperclip", "versionId": nil}}) {
				t.Fatalf("unexpected required skill snapshot: %#v", snapshot)
			}
		})
	}
}
