// Package notion implements store.Store on top of the Notion API: reading
// every already-stored posting (for dedup and expiry detection) and
// creating or updating pages. The macOS app talks to Notion directly over
// HTTP for its own read/write needs (see macapp/), so this package is
// Go-side only.
package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jomei/notionapi"

	"github.com/2miwon/notify-me/internal/job"
	"github.com/2miwon/notify-me/internal/store"
)

// maxRichTextLen is Notion's per-rich-text-object content length limit.
const maxRichTextLen = 1900 // a little under the API's 2000, for safety

// maxDescriptionBlocks caps how many paragraph blocks CreatePosting will
// send for one description, matching the API's 100-block-per-request
// limit with headroom — a description that long is not expected in
// practice.
const maxDescriptionBlocks = 90

type Client struct {
	api        *notionapi.Client
	databaseID notionapi.DatabaseID
	// token is kept alongside the notionapi client (which doesn't expose
	// it back out) because EnsureSchema needs to make one raw HTTP call
	// the typed client can't express — see schema_setup.go.
	token string
}

// New builds a client. token is a Notion internal integration secret
// (starts with "secret_" or "ntn_"); databaseID is the 32-char ID from the
// database's URL. The integration must have "Update content" capability
// (not just Read) enabled, and must be shared with the database in
// Notion (Database -> ... -> Connections) or every call below fails.
func New(token, databaseID string) *Client {
	return &Client{
		api:        notionapi.NewClient(notionapi.Token(token)),
		databaseID: notionapi.DatabaseID(databaseID),
		token:      token,
	}
}

func (c *Client) Name() string { return "notion" }

// ExistingPostings returns every stored posting keyed by URL.
func (c *Client) ExistingPostings(ctx context.Context) (map[string]store.ExistingPosting, error) {
	result := make(map[string]store.ExistingPosting)

	var cursor notionapi.Cursor
	for {
		resp, err := c.api.Database.Query(ctx, c.databaseID, &notionapi.DatabaseQueryRequest{
			StartCursor: cursor,
			PageSize:    100,
		})
		if err != nil {
			return result, fmt.Errorf("query database: %w", err)
		}

		for _, page := range resp.Results {
			urlProp, ok := page.Properties[PropURL].(*notionapi.URLProperty)
			if !ok || urlProp.URL == "" {
				continue
			}

			site := ""
			if siteProp, ok := page.Properties[PropSite].(*notionapi.SelectProperty); ok {
				site = siteProp.Select.Name
			}

			expired := false
			if expiredProp, ok := page.Properties[PropExpired].(*notionapi.CheckboxProperty); ok {
				expired = expiredProp.Checkbox
			}

			result[urlProp.URL] = store.ExistingPosting{
				ID:                 string(page.ID),
				Site:               site,
				Expired:            expired,
				MinimumDegree:      selectValue(page.Properties, PropMinimumDegree),
				CareerLevel:        selectValue(page.Properties, PropCareerLevel),
				MinYearsExperience: numberValue(page.Properties, PropMinYearsExperience),
			}
		}

		if !resp.HasMore {
			break
		}
		cursor = notionapi.Cursor(resp.NextCursor)
	}

	return result, nil
}

func selectValue(properties notionapi.Properties, name string) string {
	prop, ok := properties[name].(*notionapi.SelectProperty)
	if !ok {
		return ""
	}
	return prop.Select.Name
}

func numberValue(properties notionapi.Properties, name string) *int {
	prop, ok := properties[name].(*notionapi.NumberProperty)
	if !ok {
		return nil
	}
	value := int(prop.Number)
	return &value
}

// CreatePosting adds one new row for p. Seen/Bookmarked/Hidden/Expired
// all start false — the macOS app and the crawler's own expiry sweep are
// the only things that ever flip them afterward.
func (c *Client) CreatePosting(ctx context.Context, p job.Posting) error {
	props := notionapi.Properties{
		PropTitle: notionapi.TitleProperty{
			Title: []notionapi.RichText{{Text: &notionapi.Text{Content: p.Title}}},
		},
		PropCompany: notionapi.RichTextProperty{
			RichText: []notionapi.RichText{{Text: &notionapi.Text{Content: p.Company}}},
		},
		PropURL: notionapi.URLProperty{
			URL: p.URL,
		},
		PropSite: notionapi.SelectProperty{
			Select: notionapi.Option{Name: p.Site},
		},
		PropLocation: notionapi.RichTextProperty{
			RichText: []notionapi.RichText{{Text: &notionapi.Text{Content: p.Location}}},
		},
		PropFirstSeen: notionapi.DateProperty{
			Date: &notionapi.DateObject{Start: (*notionapi.Date)(&p.PostedAt)},
		},
		PropSeen:       notionapi.CheckboxProperty{Checkbox: false},
		PropBookmarked: notionapi.CheckboxProperty{Checkbox: false},
		PropHidden:     notionapi.CheckboxProperty{Checkbox: false},
		PropExpired:    notionapi.CheckboxProperty{Checkbox: false},
	}

	// These are only set when the source adapter actually provides them
	// (e.g. naver, not wanted) — an absent optional field is left out of
	// the request rather than written as an empty/zero value.
	if p.EmploymentType != "" {
		props[PropEmploymentType] = notionapi.SelectProperty{Select: notionapi.Option{Name: p.EmploymentType}}
	}
	if p.CareerLevel != "" {
		props[PropCareerLevel] = notionapi.SelectProperty{Select: notionapi.Option{Name: p.CareerLevel}}
	}
	if p.ApplicationStart != nil {
		props[PropApplicationStart] = notionapi.DateProperty{Date: &notionapi.DateObject{Start: (*notionapi.Date)(p.ApplicationStart)}}
	}
	if p.ApplicationDeadline != nil {
		props[PropApplicationDeadline] = notionapi.DateProperty{Date: &notionapi.DateObject{Start: (*notionapi.Date)(p.ApplicationDeadline)}}
	}
	if p.MinYearsExperience != nil {
		props[PropMinYearsExperience] = notionapi.NumberProperty{Number: float64(*p.MinYearsExperience)}
	}
	if p.MinimumDegree != "" {
		props[PropMinimumDegree] = notionapi.SelectProperty{Select: notionapi.Option{Name: p.MinimumDegree}}
	}

	_, err := c.api.Page.Create(ctx, &notionapi.PageCreateRequest{
		Parent: notionapi.Parent{
			DatabaseID: c.databaseID,
		},
		Properties: props,
		Children:   descriptionBlocks(p.Description),
	})
	if err != nil {
		return fmt.Errorf("create page for %s: %w", p.URL, err)
	}
	return nil
}

func (c *Client) UpdateMinimumDegree(ctx context.Context, id, degree string) error {
	_, err := c.api.Page.Update(ctx, notionapi.PageID(id), &notionapi.PageUpdateRequest{
		Properties: notionapi.Properties{
			PropMinimumDegree: notionapi.SelectProperty{Select: notionapi.Option{Name: degree}},
		},
	})
	if err != nil {
		return fmt.Errorf("update minimum degree for %s: %w", id, err)
	}
	return nil
}

func (c *Client) UpdateCareerLevel(ctx context.Context, id, level string) error {
	return c.updateSelectProperty(ctx, id, PropCareerLevel, level)
}

func (c *Client) UpdateMinYearsExperience(ctx context.Context, id string, years int) error {
	_, err := c.api.Page.Update(ctx, notionapi.PageID(id), &notionapi.PageUpdateRequest{
		Properties: notionapi.Properties{
			PropMinYearsExperience: notionapi.NumberProperty{Number: float64(years)},
		},
	})
	if err != nil {
		return fmt.Errorf("update minimum experience for %s: %w", id, err)
	}
	return nil
}

func (c *Client) updateSelectProperty(ctx context.Context, id, property, value string) error {
	// notionapi's SelectProperty uses a non-pointer Option and therefore
	// cannot encode the null required to clear an existing select value.
	// Use the small raw request below only for this clear/update operation.
	payload := map[string]interface{}{
		"properties": map[string]interface{}{
			property: map[string]interface{}{"select": nil},
		},
	}
	if value != "" {
		payload["properties"].(map[string]interface{})[property] = map[string]interface{}{
			"select": map[string]string{"name": value},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, "https://api.notion.com/v1/pages/"+id, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", notionAPIVersion)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update %s for %s: unexpected status %d: %s", property, id, resp.StatusCode, responseBody)
	}
	return nil
}

// descriptionBlocks turns a posting's free-text body into paragraph
// blocks for the page's content — Notion has no "long text" property
// type, so this is the idiomatic place for it (visible when the page is
// opened, same as pasting the description in by hand). Returns nil for
// an empty description, which Children accepts as "no body content".
func descriptionBlocks(description string) []notionapi.Block {
	if description == "" {
		return nil
	}

	var blocks []notionapi.Block
	for _, section := range strings.Split(description, "\n\n") {
		for _, chunk := range chunkString(section, maxRichTextLen) {
			if len(blocks) >= maxDescriptionBlocks {
				return blocks
			}
			blocks = append(blocks, notionapi.ParagraphBlock{
				BasicBlock: notionapi.BasicBlock{
					Object: notionapi.ObjectTypeBlock,
					Type:   notionapi.BlockTypeParagraph,
				},
				Paragraph: notionapi.Paragraph{
					RichText: []notionapi.RichText{{Text: &notionapi.Text{Content: chunk}}},
				},
			})
		}
	}
	return blocks
}

func chunkString(s string, size int) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var chunks []string
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}

// DeletePosting archives the page, which is Notion's recoverable delete.
// It no longer appears in database queries or the macOS app, but can be
// restored from Notion's trash if needed.
func (c *Client) DeletePosting(ctx context.Context, id string) error {
	_, err := c.api.Page.Update(ctx, notionapi.PageID(id), &notionapi.PageUpdateRequest{
		Archived: true,
	})
	if err != nil {
		return fmt.Errorf("archive expired posting %s: %w", id, err)
	}
	return nil
}
