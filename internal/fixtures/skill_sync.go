package fixtures

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var skillVersionGUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// SkillSyncSnapshot enforces agentSkillSyncSchema at the mock HTTP boundary and
// returns the server's normalized snapshot, including both response projections.
func SkillSyncSnapshot(body io.Reader) (map[string]interface{}, error) {
	var request map[string]interface{}
	if err := json.NewDecoder(body).Decode(&request); err != nil {
		return nil, err
	}
	mode, _ := request["mode"].(string)
	if mode != "add" && mode != "remove" && mode != "replace" {
		return nil, fmt.Errorf("invalid skill assignment mode")
	}
	selections, ok := request["desiredSkills"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("desiredSkills must be an array")
	}
	keys := make([]string, 0, len(selections))
	entries := make([]map[string]interface{}, 0, len(selections))
	for _, selection := range selections {
		key, isString := selection.(string)
		var version interface{}
		if !isString {
			entry, isObject := selection.(map[string]interface{})
			if !isObject {
				return nil, fmt.Errorf("skill selection must be a string or object")
			}
			key, _ = entry["key"].(string)
			var present bool
			version, present = entry["versionId"]
			if !present {
				return nil, fmt.Errorf("versionId is required on object selections")
			}
			if version != nil {
				id, isString := version.(string)
				if !isString || !skillVersionGUID.MatchString(id) {
					return nil, fmt.Errorf("versionId must be a GUID or null")
				}
			}
		}
		if key == "" {
			return nil, fmt.Errorf("skill key must be a non-empty string")
		}
		key = strings.TrimSpace(key)
		keys = append(keys, key)
		entries = append(entries, map[string]interface{}{"key": key, "versionId": version})
	}
	return map[string]interface{}{
		"adapterType": "antigravity_local", "supported": true, "mode": mode,
		"desiredSkills": keys, "desiredSkillEntries": entries,
	}, nil
}
