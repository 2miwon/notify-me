// Command notion-init provisions a Notion database's schema for the
// crawler, instead of adding all 14 properties by hand in the Notion UI.
// Safe to run more than once — it only adds what's missing and never
// touches an existing property.
//
// Required environment variables:
//
//	NOTION_TOKEN       Notion internal integration secret
//	NOTION_DATABASE_ID Target database ID
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/2miwon/notify-me/internal/notion"
)

func main() {
	token := os.Getenv("NOTION_TOKEN")
	databaseID := os.Getenv("NOTION_DATABASE_ID")
	if token == "" || databaseID == "" {
		log.Fatal("NOTION_TOKEN and NOTION_DATABASE_ID must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	added, renamedTitle, err := notion.New(token, databaseID).EnsureSchema(ctx)
	if err != nil {
		log.Fatalf("notion-init: %v", err)
	}

	if renamedTitle {
		log.Println("renamed the database's title property to \"Title\"")
	}
	if len(added) == 0 {
		log.Println("no properties to add — schema already matches")
		return
	}
	log.Printf("added %d propert%s: %v", len(added), plural(len(added)), added)
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
