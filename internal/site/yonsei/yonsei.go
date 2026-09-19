// Package yonsei implements the authenticated "추천채용" listing from
// Career Yonsei. Credentials are read only from YONSEI_ID/YONSEI_PW at run
// time; they are never persisted, logged, or included in error messages.
package yonsei

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	baseURL      = "https://career.yonsei.ac.kr"
	recommendURL = baseURL + "/ptfol/career/program/rcrt/totRec/findCampRcrtList.do?paginationInfo.currentPageNo=1&tabType=reco&midTabType=card&orderTy=input&searchYn=Y&statusSh=%EC%A7%84%ED%96%89&compType=0000&placeSh=0000&paginationInfo.recordCountPerPage=20"
	userAgent    = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
)

var (
	challengePattern = regexp.MustCompile(`var\s+ssoChallenge\s*=\s*'([^']+)'`)
	rsaPattern       = regexp.MustCompile(`rsa\.setPublic\(\s*'([0-9a-fA-F]+)'\s*,\s*'([0-9a-fA-F]+)'`)
)

type Adapter struct{ client *http.Client }

func New() *Adapter             { return &Adapter{} }
func (a *Adapter) Name() string { return "yonsei" }

func Configured() bool { return os.Getenv("YONSEI_ID") != "" && os.Getenv("YONSEI_PW") != "" }

func (a *Adapter) Fetch() ([]job.Posting, error) {
	client, err := a.authenticatedClient()
	if err != nil {
		return nil, err
	}
	doc, _, err := getDocument(client, recommendURL)
	if err != nil {
		return nil, err
	}
	if isLoginPage(doc) {
		return nil, fmt.Errorf("yonsei: login was not accepted")
	}
	return parseListings(doc)
}

func (a *Adapter) authenticatedClient() (*http.Client, error) {
	userID, password := os.Getenv("YONSEI_ID"), os.Getenv("YONSEI_PW")
	if userID == "" || password == "" {
		return nil, fmt.Errorf("yonsei: YONSEI_ID and YONSEI_PW must be set")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second}

	// The protected listing redirects to the Career Yonsei login gateway.
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequest(http.MethodGet, recommendURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := noRedirect.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		resp.Body.Close()
		return nil, fmt.Errorf("yonsei: expected login redirect, got status %d", resp.StatusCode)
	}
	loginURL, err := resp.Location()
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("yonsei: read login redirect: %w", err)
	}
	if !loginURL.IsAbs() {
		loginURL = req.URL.ResolveReference(loginURL)
	}

	loginDoc, loginPageURL, err := getDocument(client, loginURL.String())
	if err != nil {
		return nil, err
	}
	// Career Yonsei -> Yonsei SSO login page.
	ssoDoc, ssoURL, err := submitForm(client, loginDoc, loginPageURL, "#ssoLoginForm", nil)
	if err != nil {
		return nil, fmt.Errorf("yonsei: start SSO: %w", err)
	}
	// Career Yonsei -> infra.yonsei.ac.kr SSO login page.
	loginDoc, loginPageURL, err = submitForm(client, ssoDoc, ssoURL, "#frmSSO", nil)
	if err != nil {
		return nil, fmt.Errorf("yonsei: open SSO login: %w", err)
	}

	challenge, key, err := loginEncryption(loginDoc)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(struct {
		UserID       string `json:"userid"`
		Password     string `json:"userpw"`
		SSOChallenge string `json:"ssoChallenge"`
	}{userID, password, challenge})
	if err != nil {
		return nil, err
	}
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, key, payload)
	if err != nil {
		return nil, fmt.Errorf("yonsei: encrypt credentials: %w", err)
	}

	authDoc, authURL, err := submitForm(client, loginDoc, loginPageURL, "#ssoLoginForm", url.Values{"E2": {hex.EncodeToString(ciphertext)}})
	if err != nil {
		return nil, fmt.Errorf("yonsei: submit credentials: %w", err)
	}
	// Successful SSO hands the browser back through one or more auto-submitted
	// forms. Follow only the forms served by the authenticated Yonsei domains.
	for range 4 {
		form := authDoc.Find("form").First()
		if form.Length() == 0 {
			break
		}
		nextDoc, nextURL, formErr := submitSelection(client, authDoc, authURL, form, nil)
		if formErr != nil {
			return nil, fmt.Errorf("yonsei: complete SSO: %w", formErr)
		}
		authDoc, authURL = nextDoc, nextURL
	}
	return client, nil
}

func getDocument(client *http.Client, target string) (*goquery.Document, *url.URL, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	return doc, resp.Request.URL, err
}

func submitForm(client *http.Client, doc *goquery.Document, pageURL *url.URL, selector string, values url.Values) (*goquery.Document, *url.URL, error) {
	form := doc.Find(selector).First()
	if form.Length() == 0 {
		return nil, nil, fmt.Errorf("form %s not found", selector)
	}
	return submitSelection(client, doc, pageURL, form, values)
}

func submitSelection(client *http.Client, _ *goquery.Document, pageURL *url.URL, form *goquery.Selection, overrides url.Values) (*goquery.Document, *url.URL, error) {
	action := strings.TrimSpace(form.AttrOr("action", ""))
	if action == "" {
		return nil, nil, fmt.Errorf("form action missing")
	}
	target, err := pageURL.Parse(action)
	if err != nil {
		return nil, nil, err
	}
	if target.Host != "career.yonsei.ac.kr" && target.Host != "infra.yonsei.ac.kr" {
		return nil, nil, fmt.Errorf("unexpected SSO form host %q", target.Host)
	}
	values := url.Values{}
	form.Find("input[name]").Each(func(_ int, s *goquery.Selection) {
		name := s.AttrOr("name", "")
		if name != "" {
			values.Set(name, s.AttrOr("value", ""))
		}
	})
	for name, value := range overrides {
		values[name] = append([]string(nil), value...)
	}
	req, err := http.NewRequest(http.MethodPost, target.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	next, err := goquery.NewDocumentFromReader(resp.Body)
	return next, resp.Request.URL, err
}

func loginEncryption(doc *goquery.Document) (string, *rsa.PublicKey, error) {
	source, err := doc.Html()
	if err != nil {
		return "", nil, err
	}
	challengeMatch, keyMatch := challengePattern.FindStringSubmatch(source), rsaPattern.FindStringSubmatch(source)
	if len(challengeMatch) != 2 || len(keyMatch) != 3 {
		return "", nil, fmt.Errorf("yonsei: SSO encryption parameters not found")
	}
	modulus := new(big.Int)
	if _, ok := modulus.SetString(keyMatch[1], 16); !ok {
		return "", nil, fmt.Errorf("yonsei: invalid RSA modulus")
	}
	exponent := new(big.Int)
	if _, ok := exponent.SetString(keyMatch[2], 16); !ok || !exponent.IsInt64() {
		return "", nil, fmt.Errorf("yonsei: invalid RSA exponent")
	}
	return challengeMatch[1], &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}, nil
}

func isLoginPage(doc *goquery.Document) bool {
	return doc.Find("#ssoLoginForm, #etcLoginForm").Length() > 0
}

func parseListings(doc *goquery.Document) ([]job.Posting, error) {
	// The reco tab's cards link to individual recruitment detail pages. Keep
	// parsing scoped to those paths so navigation/footer links never become
	// phantom postings.
	seen := map[string]bool{}
	var postings []job.Posting
	doc.Find("a[href]").Each(func(_ int, link *goquery.Selection) {
		href := link.AttrOr("href", "")
		if !strings.Contains(href, "/rcrt/") || !strings.Contains(href, ".do") || seen[href] {
			return
		}
		title := normalize(link.Text())
		if title == "" || strings.Contains(title, "목록") {
			return
		}
		seen[href] = true
		u, err := url.Parse(href)
		if err != nil {
			return
		}
		if !u.IsAbs() {
			u, _ = url.Parse(baseURL + "/" + strings.TrimPrefix(href, "/"))
		}
		postings = append(postings, job.Posting{Site: "yonsei", ExternalID: u.String(), Title: title, Company: "연세대학교 추천채용", URL: u.String(), PostedAt: time.Now().UTC()})
	})
	if len(postings) == 0 {
		return nil, fmt.Errorf("yonsei: no recommended-recruitment cards found")
	}
	return postings, nil
}

func normalize(value string) string { return strings.Join(strings.Fields(value), " ") }
