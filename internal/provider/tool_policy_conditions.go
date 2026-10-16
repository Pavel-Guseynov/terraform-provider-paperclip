package provider

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
)

// These constraints mirror the pinned native strict condition validator. Loose
// selectors remain loose; predicate values remain arbitrary JSON.
type conditionField struct {
	kind string
	max  int
}

var conditionGroups = map[string]map[string]conditionField{
	"arguments":       {"fieldEquals": {"record", 0}, "fieldNotEquals": {"record", 0}, "fieldIn": {"in", 0}, "fieldMatches": {"matches", 0}, "fieldExists": {"field[]", 100}, "fieldAbsent": {"field[]", 100}},
	"actor":           {"actorType": {"actor", 0}, "actorTypes": {"actor[]", 20}, "agentId": {"uuid", 0}, "agentIds": {"uuid[]", 100}},
	"context":         {"projectId": {"uuid", 0}, "projectIds": {"uuid[]", 100}, "routineId": {"uuid", 0}, "routineIds": {"uuid[]", 100}, "issueId": {"uuid", 0}, "issueIds": {"uuid[]", 100}, "requireIssue": {"bool", 0}, "requireProject": {"bool", 0}, "requireRoutine": {"bool", 0}},
	"risk":            {"levels": {"risk[]", 20}, "max": {"risk", 0}, "isWrite": {"bool", 0}, "isDestructive": {"bool", 0}},
	"credentialScope": {"applicationId": {"uuid", 0}, "applicationIds": {"uuid[]", 100}, "connectionId": {"uuid", 0}, "connectionIds": {"uuid[]", 100}, "catalogEntryId": {"uuid", 0}, "catalogEntryIds": {"uuid[]", 100}, "applicationKey": {"text", 0}, "applicationKeys": {"text[]", 100}, "providerType": {"text", 0}, "providerTypes": {"text[]", 100}},
	"trustBoundary":   {"providerType": {"text", 0}, "providerTypes": {"text[]", 100}, "applicationKey": {"text", 0}, "applicationKeys": {"text[]", 100}, "remoteHttpOnly": {"bool", 0}, "paperclipSelfOnly": {"bool", 0}},
	"timeWindow":      {"startAt": {"datetime", 0}, "endAt": {"datetime", 0}, "daysOfWeekUtc": {"day[]", 7}, "startHourUtc": {"hour", 23}, "endHourUtc": {"hour", 24}},
}

func validateToolConditions(conditions map[string]interface{}) error {
	if len(conditions) == 0 {
		return fmt.Errorf("conditions requires at least one supported condition group; omit it to clear")
	}
	for group, value := range conditions {
		schemaGroup := group
		if group == "args" {
			schemaGroup = "arguments"
		}
		fields, ok := conditionGroups[schemaGroup]
		if !ok {
			return fmt.Errorf("unsupported conditions group %s", group)
		}
		object, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("conditions.%s must be an object", group)
		}
		nonempty := false
		for key, v := range object {
			rule, ok := fields[key]
			if !ok {
				return fmt.Errorf("unsupported conditions.%s.%s", group, key)
			}
			if !validConditionField(v, rule) {
				return fmt.Errorf("invalid conditions.%s.%s: expected native %s constraints", group, key, rule.kind)
			}
			if !isEmptyCollectionValue(v) {
				nonempty = true
			}
			if group == "arguments" && key == "fieldMatches" {
				for _, pattern := range v.(map[string]interface{}) {
					if !safeToolRegex(pattern.(string)) {
						return fmt.Errorf("conditions.arguments.fieldMatches includes an unsafe regular expression")
					}
				}
			}
		}
		if (schemaGroup == "arguments" || schemaGroup == "timeWindow") && !nonempty {
			return fmt.Errorf("conditions.%s requires at least one predicate or bound", group)
		}
	}
	return nil
}
func isEmptyCollectionValue(value interface{}) bool {
	switch v := value.(type) {
	case map[string]interface{}:
		return len(v) == 0
	case []interface{}:
		return len(v) == 0
	}
	return false
}
func validConditionField(value interface{}, rule conditionField) bool {
	if strings.HasSuffix(rule.kind, "[]") {
		list, ok := value.([]interface{})
		if !ok || len(list) > rule.max {
			return false
		}
		for _, item := range list {
			if !validConditionField(item, conditionField{kind: strings.TrimSuffix(rule.kind, "[]")}) {
				return false
			}
		}
		return true
	}
	s, sok := value.(string)
	switch rule.kind {
	case "bool":
		_, ok := value.(bool)
		return ok
	case "uuid":
		return sok && toolUUID.MatchString(s)
	case "actor":
		return sok && (s == "agent" || s == "user" || s == "system" || s == "plugin")
	case "risk":
		return sok && toolRiskLevels[s]
	case "text", "field":
		limit := 160
		if rule.kind == "field" {
			limit = 120
		}
		return sok && s == strings.TrimSpace(s) && len([]rune(s)) > 0 && len([]rune(s)) <= limit
	case "datetime":
		if !sok {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, s)
		return err == nil
	case "hour", "day":
		n, ok := value.(float64)
		max := float64(rule.max)
		if rule.kind == "day" {
			max = 6
		}
		return ok && n == math.Trunc(n) && n >= 0 && n <= max
	default: // The remaining declared field kinds are record, in and matches.
		object, ok := value.(map[string]interface{})
		if !ok {
			return false
		}
		for field, v := range object {
			if !validConditionField(field, conditionField{kind: "field"}) {
				return false
			}
			if rule.kind == "in" {
				list, ok := v.([]interface{})
				if !ok || len(list) < 1 || len(list) > 100 {
					return false
				}
			}
			if rule.kind == "matches" {
				text, ok := v.(string)
				if !ok || text != strings.TrimSpace(text) || len([]rune(text)) == 0 || len([]rune(text)) > 500 {
					return false
				}
			}
		}
		return true
	}
}

var unsafeToolBackref = regexp.MustCompile(`\\[1-9]`)
var unsafeToolAlternation = regexp.MustCompile(`(^|[^\\])\|`)
var unsafeToolNested = regexp.MustCompile(`\((?:[^()\\]|\\.)*[+*](?:[^()\\]|\\.)*\)[+*{]`)

func safeToolRegex(pattern string) bool {
	return len(pattern) <= 256 && !unsafeToolBackref.MatchString(pattern) && !unsafeToolAlternation.MatchString(pattern) && !unsafeToolNested.MatchString(pattern)
}

func validateToolReferenceObjects(ctx context.Context, c *client.Client, companyID string, selectors, conditions map[string]interface{}) error {
	objects := []map[string]interface{}{selectors}
	for _, group := range []string{"actor", "context", "credentialScope"} {
		if object, ok := conditions[group].(map[string]interface{}); ok {
			objects = append(objects, object)
		}
	}
	seen := map[string]bool{}
	for _, object := range objects {
		for key, kind := range map[string]string{"agentId": "agent", "projectId": "project", "routineId": "routine", "issueId": "issue", "applicationId": "application", "connectionId": "connection", "catalogEntryId": "catalog_entry", "gatewayId": "gateway"} {
			ids := []interface{}{}
			if v, ok := object[key]; ok {
				ids = append(ids, v)
			}
			if v, ok := object[key+"s"]; ok {
				list, ok := v.([]interface{})
				if !ok {
					return fmt.Errorf("%s selector must be an array", key+"s")
				}
				ids = append(ids, list...)
			}
			for _, raw := range ids {
				id, ok := raw.(string)
				if !ok || id == "" {
					return fmt.Errorf("%s selector requires a nonempty identity", key)
				}
				if !seen[kind+id] {
					if err := c.AssertToolTarget(ctx, companyID, kind, id); err != nil {
						return err
					}
					seen[kind+id] = true
				}
			}
		}
	}
	return nil
}
