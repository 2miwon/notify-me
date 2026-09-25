// Package sheets implements store.Store on top of the Google Sheets API,
// as an alternative to Notion. One posting is one row; columns match
// internal/notion's schema so both backends carry the same information.
package sheets

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"

	"github.com/2miwon/notify-me/internal/job"
	"github.com/2miwon/notify-me/internal/store"
)

// Column layout. Seen/Bookmarked/Hidden exist here so a spreadsheet
// backend carries the same fields Notion does, but — same as Notion —
// only a client app (not the crawler) ever writes them. EmploymentType/
// CareerLevel/ApplicationStart/ApplicationDeadline/Description are left
// blank for adapters that don't provide them (e.g. wanted) — same
// "absent, not zero" convention as the Notion backend.
const (
	colTitle = iota
	colCompany
	colURL
	colSite
	colLocation
	colFirstSeen
	colSeen
	colBookmarked
	colHidden
	colExpired
	colEmploymentType
	colCareerLevel
	colApplicationStart
	colApplicationDeadline
	colDescription
	colMinYearsExperience
	colMinimumDegree
	// Applied is app-owned like Seen/Bookmarked/Hidden, but appended last
	// rather than grouped with them so existing sheets keep their layout.
	colApplied
)

const defaultSheetName = "Postings"

var header = []interface{}{
	"Title", "Company", "URL", "Site", "Location", "First Seen",
	"Seen", "Bookmarked", "Hidden", "Expired",
	"Employment Type", "Career Level", "Application Start", "Application Deadline",
	"Description", "Min Years Experience",
	"Minimum Degree", "Applied",
}

// lastColumn is the header's last column letter, used to build A1:<lastColumn>
// range references without hardcoding it in three places.
const lastColumn = "R"

type Client struct {
	svc           *sheets.Service
	spreadsheetID string
	sheetName     string
}

// New builds a client from a Google service account key's raw JSON
// (the whole downloaded key file content, not a path). Share the target
// spreadsheet with the service account's client_email as an Editor —
// the same idea as sharing a Notion database with a Notion integration.
// Creates a header row on first use if the sheet is empty.
func New(ctx context.Context, serviceAccountJSON []byte, spreadsheetID, sheetName string) (*Client, error) {
	svc, err := sheets.NewService(ctx, option.WithCredentialsJSON(serviceAccountJSON))
	if err != nil {
		return nil, fmt.Errorf("sheets client: %w", err)
	}
	if sheetName == "" {
		sheetName = defaultSheetName
	}

	c := &Client{svc: svc, spreadsheetID: spreadsheetID, sheetName: sheetName}
	if err := c.ensureHeader(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) Name() string { return "sheets" }

func (c *Client) ensureHeader(ctx context.Context) error {
	resp, err := c.svc.Spreadsheets.Values.Get(c.spreadsheetID, c.rangeRef("A1:"+lastColumn+"1")).
		ValueRenderOption("UNFORMATTED_VALUE").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if len(resp.Values) > 0 {
		// Older sheets already have a header row but not the columns
		// appended since (Minimum Degree, Applied). Fill in just the
		// missing trailing cells so existing user columns and formatting
		// stay untouched.
		for col := colMinimumDegree; col < len(header); col++ {
			if cellString(resp.Values[0], col) != "" {
				continue
			}
			cell := fmt.Sprintf("%c1", 'A'+col)
			_, err := c.svc.Spreadsheets.Values.Update(c.spreadsheetID, c.rangeRef(cell), &sheets.ValueRange{
				Values: [][]interface{}{{header[col]}},
			}).ValueInputOption("RAW").Context(ctx).Do()
			if err != nil {
				return fmt.Errorf("write %v header: %w", header[col], err)
			}
		}
		return nil
	}

	_, err = c.svc.Spreadsheets.Values.Update(c.spreadsheetID, c.rangeRef("A1"), &sheets.ValueRange{
		Values: [][]interface{}{header},
	}).ValueInputOption("RAW").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	return nil
}

func (c *Client) ExistingPostings(ctx context.Context) (map[string]store.ExistingPosting, error) {
	result := make(map[string]store.ExistingPosting)

	// UNFORMATTED_VALUE keeps the legacy Expired column as an actual JSON
	// boolean instead of a display string that would need reinterpreting.
	resp, err := c.svc.Spreadsheets.Values.Get(c.spreadsheetID, c.rangeRef("A2:"+lastColumn)).
		ValueRenderOption("UNFORMATTED_VALUE").Context(ctx).Do()
	if err != nil {
		return result, fmt.Errorf("read rows: %w", err)
	}

	for _, row := range resp.Values {
		url := cellString(row, colURL)
		if url == "" {
			continue
		}
		result[url] = store.ExistingPosting{
			// Use the immutable URL as the delete key. Row positions change
			// whenever another expired row is removed during this same crawl.
			ID:                 url,
			Site:               cellString(row, colSite),
			Expired:            cellBool(row, colExpired),
			Applied:            cellBool(row, colApplied),
			MinimumDegree:      cellString(row, colMinimumDegree),
			EmploymentType:     cellString(row, colEmploymentType),
			CareerLevel:        cellString(row, colCareerLevel),
			MinYearsExperience: cellInt(row, colMinYearsExperience),
		}
	}
	return result, nil
}

func (c *Client) EnsureDescription(ctx context.Context, url, description string) error {
	if strings.TrimSpace(description) == "" {
		return nil
	}
	resp, err := c.svc.Spreadsheets.Values.Get(c.spreadsheetID, c.rangeRef("A2:"+lastColumn)).
		ValueRenderOption("UNFORMATTED_VALUE").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("find row for description update: %w", err)
	}
	for i, row := range resp.Values {
		if cellString(row, colURL) != url || strings.TrimSpace(cellString(row, colDescription)) != "" {
			continue
		}
		_, err := c.svc.Spreadsheets.Values.Update(c.spreadsheetID, c.rangeRef(fmt.Sprintf("O%d", i+2)), &sheets.ValueRange{
			Values: [][]interface{}{{description}},
		}).ValueInputOption("RAW").Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("update description for %s: %w", url, err)
		}
		return nil
	}
	return nil
}

func (c *Client) UpdateMinimumDegree(ctx context.Context, url, degree string) error {
	return c.updateCellByURL(ctx, url, "Q", degree, "minimum degree")
}

func (c *Client) UpdateCareerLevel(ctx context.Context, url, level string) error {
	return c.updateCellByURL(ctx, url, "L", level, "career level")
}

func (c *Client) UpdateEmploymentType(ctx context.Context, url, employmentType string) error {
	return c.updateCellByURL(ctx, url, "K", employmentType, "employment type")
}

func (c *Client) UpdateMinYearsExperience(ctx context.Context, url string, years int) error {
	return c.updateCellByURL(ctx, url, "P", years, "minimum experience")
}

func (c *Client) MarkExpired(ctx context.Context, url string) error {
	return c.updateCellByURL(ctx, url, "J", true, "expired")
}

func (c *Client) updateCellByURL(ctx context.Context, url, column string, value interface{}, field string) error {
	resp, err := c.svc.Spreadsheets.Values.Get(c.spreadsheetID, c.rangeRef("A2:"+lastColumn)).
		ValueRenderOption("UNFORMATTED_VALUE").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("find row for %s update: %w", field, err)
	}
	for i, row := range resp.Values {
		if cellString(row, colURL) != url {
			continue
		}
		_, err := c.svc.Spreadsheets.Values.Update(c.spreadsheetID, c.rangeRef(fmt.Sprintf("%s%d", column, i+2)), &sheets.ValueRange{
			Values: [][]interface{}{{value}},
		}).ValueInputOption("RAW").Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("update %s for %s: %w", field, url, err)
		}
		return nil
	}
	return nil
}

func (c *Client) CreatePosting(ctx context.Context, p job.Posting) error {
	row := make([]interface{}, len(header))
	row[colTitle] = p.Title
	row[colCompany] = p.Company
	row[colURL] = p.URL
	row[colSite] = p.Site
	row[colLocation] = p.Location
	row[colFirstSeen] = p.PostedAt.Format(time.RFC3339)
	row[colSeen] = false
	row[colBookmarked] = false
	row[colHidden] = false
	row[colApplied] = false
	row[colExpired] = false
	row[colEmploymentType] = p.EmploymentType
	row[colCareerLevel] = p.CareerLevel
	row[colApplicationStart] = formatOptionalTime(p.ApplicationStart)
	row[colApplicationDeadline] = formatOptionalTime(p.ApplicationDeadline)
	row[colDescription] = p.Description
	if p.MinYearsExperience != nil {
		row[colMinYearsExperience] = *p.MinYearsExperience
	}
	row[colMinimumDegree] = p.MinimumDegree

	_, err := c.svc.Spreadsheets.Values.Append(c.spreadsheetID, c.rangeRef("A:"+lastColumn), &sheets.ValueRange{
		Values: [][]interface{}{row},
	}).ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("append row for %s: %w", p.URL, err)
	}
	return nil
}

func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// DeletePosting removes the matching row entirely. The URL is used as the
// identifier because physical row numbers shift after each deletion.
func (c *Client) DeletePosting(ctx context.Context, url string) error {
	resp, err := c.svc.Spreadsheets.Values.Get(c.spreadsheetID, c.rangeRef("A2:"+lastColumn)).
		ValueRenderOption("UNFORMATTED_VALUE").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("find expired row: %w", err)
	}

	rowNum := 0
	for i, row := range resp.Values {
		if cellString(row, colURL) == url {
			rowNum = i + 2
			break
		}
	}
	if rowNum == 0 {
		// Another delete in this run may already have removed it. Treat that
		// as success: the desired end state (no row) is already true.
		return nil
	}

	sheetID, err := c.sheetID(ctx)
	if err != nil {
		return err
	}
	_, err = c.svc.Spreadsheets.BatchUpdate(c.spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{{
			DeleteDimension: &sheets.DeleteDimensionRequest{Range: &sheets.DimensionRange{
				SheetId: sheetID, Dimension: "ROWS",
				StartIndex: int64(rowNum - 1), EndIndex: int64(rowNum),
			}},
		}},
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("delete expired row for %s: %w", url, err)
	}
	return nil
}

func (c *Client) sheetID(ctx context.Context) (int64, error) {
	spreadsheet, err := c.svc.Spreadsheets.Get(c.spreadsheetID).
		Fields("sheets.properties(sheetId,title)").Context(ctx).Do()
	if err != nil {
		return 0, fmt.Errorf("get sheet metadata: %w", err)
	}
	for _, sheet := range spreadsheet.Sheets {
		if sheet.Properties != nil && sheet.Properties.Title == c.sheetName {
			return sheet.Properties.SheetId, nil
		}
	}
	return 0, fmt.Errorf("sheet %q not found", c.sheetName)
}

func (c *Client) rangeRef(a1 string) string {
	return fmt.Sprintf("%s!%s", c.sheetName, a1)
}

// cellString and cellBool exist because the Sheets API returns each cell
// typed as whatever JSON value matches its content (string, float64, or
// bool) — a naive `.(string)` assertion silently zero-values a real
// boolean cell instead of failing loudly, which is exactly the bug that
// would make expiry detection always read as false.
func cellString(row []interface{}, idx int) string {
	if idx >= len(row) {
		return ""
	}
	s, _ := row[idx].(string)
	return s
}

func cellBool(row []interface{}, idx int) bool {
	if idx >= len(row) {
		return false
	}
	switch v := row[idx].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "TRUE")
	default:
		return false
	}
}

func cellInt(row []interface{}, idx int) *int {
	if idx >= len(row) {
		return nil
	}
	switch value := row[idx].(type) {
	case float64:
		result := int(value)
		return &result
	case string:
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return &parsed
		}
	}
	return nil
}
