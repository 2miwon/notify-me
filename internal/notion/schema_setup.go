package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jomei/notionapi"
)

const notionAPIVersion = "2022-06-28"

// requiredProperties is the full non-title schema this package expects —
// used both by EnsureSchema (to provision a blank database) and as the
// single source of truth for what type each property should be. Keys
// match the Prop* constants in schema.go; values are Notion's own
// property type identifiers.
var requiredProperties = map[string]string{
	PropCompany:             "rich_text",
	PropURL:                 "url",
	PropSite:                "select",
	PropLocation:            "rich_text",
	PropFirstSeen:           "date",
	PropSeen:                "checkbox",
	PropBookmarked:          "checkbox",
	PropHidden:              "checkbox",
	PropExpired:             "checkbox",
	PropEmploymentType:      "select",
	PropCareerLevel:         "select",
	PropApplicationStart:    "date",
	PropApplicationDeadline: "date",
	PropMinYearsExperience:  "number",
	PropMinimumDegree:       "select",
}

// EnsureSchema provisions a database for this package: renames its title
// property to "Title" if it isn't already (a brand-new Notion database's
// title property is normally called "Name"), and adds any of
// requiredProperties that are missing. Existing properties — including
// ones that happen to already be named right — are left untouched, so
// this is safe to run against a database that's already fully set up
// (it will just report nothing to add) or partially set up.
//
// Renaming a property only works by sending {"name": "<new>"} keyed by
// its *current* name, which notionapi's typed PropertyConfig interface
// has no way to express — so this makes one raw HTTP call instead of
// going through c.api for that part. Reading the current schema still
// goes through the typed client.
func (c *Client) EnsureSchema(ctx context.Context) (added []string, renamedTitle bool, err error) {
	db, err := c.api.Database.Get(ctx, c.databaseID)
	if err != nil {
		return nil, false, fmt.Errorf("get database: %w", err)
	}

	titleName := ""
	for name, prop := range db.Properties {
		if prop.GetType() == notionapi.PropertyConfigTypeTitle {
			titleName = name
			break
		}
	}
	if titleName == "" {
		return nil, false, fmt.Errorf("database has no title property — this shouldn't be possible for a real Notion database")
	}

	patch := map[string]interface{}{}

	if titleName != PropTitle {
		patch[titleName] = map[string]interface{}{"name": PropTitle}
		renamedTitle = true
	}

	for name, propType := range requiredProperties {
		if _, exists := db.Properties[name]; exists {
			continue
		}
		patch[name] = map[string]interface{}{propType: propTypeConfig(propType)}
		added = append(added, name)
	}

	if len(patch) == 0 {
		return added, renamedTitle, nil
	}

	if err := c.patchDatabaseSchema(ctx, patch); err != nil {
		return nil, false, err
	}
	return added, renamedTitle, nil
}

// propTypeConfig returns the config object a property schema needs beyond
// its bare type — empty for most types, but Notion's "number" type
// requires a "format" (e.g. "number", "dollar", "percent") or the API
// rejects the request.
func propTypeConfig(propType string) map[string]interface{} {
	if propType == "number" {
		return map[string]interface{}{"format": "number"}
	}
	return map[string]interface{}{}
}

func (c *Client) patchDatabaseSchema(ctx context.Context, properties map[string]interface{}) error {
	payload, err := json.Marshal(map[string]interface{}{"properties": properties})
	if err != nil {
		return fmt.Errorf("marshal schema patch: %w", err)
	}

	url := "https://api.notion.com/v1/databases/" + string(c.databaseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", notionAPIVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("patch database schema: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("patch database schema: unexpected status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
