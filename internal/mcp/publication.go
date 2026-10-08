package mcp

import "github.com/monoes/mono-agent/internal/publication"

func publicationTools() []tool {
	return []tool{
		{name: "publication_list", description: "List this profile's published posts, comments, replies and other content, newest first.", schema: objSchema(publication.FilterSchema()), annotations: map[string]bool{"readOnlyHint": true}, handler: chatToolHandler("list_publications")},
		{name: "publication_get", description: "Read a publication with its content, live URL and agent/workflow source.", schema: objSchema(map[string]interface{}{"id": strParam("Publication ID")}, "id"), annotations: map[string]bool{"readOnlyHint": true}, handler: chatToolHandler("get_publication")},
		{name: "publication_stats", description: "Count this profile's publications by platform and kind.", schema: objSchema(nil), annotations: map[string]bool{"readOnlyHint": true}, handler: chatToolHandler("publication_stats")},
		{name: "publication_register", description: "Register content already successfully published outside mono-agent's built-in nodes. Does not publish. Use remote identity or idempotency_key to register a result once.", schema: objSchema(publication.EntrySchema(), "platform", "kind"), mutating: true, annotations: map[string]bool{"readOnlyHint": false}, handler: chatToolHandler("register_publication")},
		{name: "publication_delete", description: "Remove a publication from this profile's history (the remote post is untouched). Operator sessions only: refused inside an agent session.", schema: objSchema(map[string]interface{}{"id": strParam("Publication ID")}, "id"), mutating: true, annotations: map[string]bool{"destructiveHint": true}, handler: chatToolHandler("delete_publication")},
		{name: "publication_redact", description: "Replace a publication's body (and title if title is true) with [redacted], keeping the entry, URL, kind, platform and timestamps. Operator sessions only: refused inside an agent session.", schema: objSchema(map[string]interface{}{"id": strParam("Publication ID"), "title": boolParam("Also redact the title")}, "id"), mutating: true, annotations: map[string]bool{"destructiveHint": true}, handler: chatToolHandler("redact_publication")},
	}
}
