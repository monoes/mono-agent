package service

import (
	"context"
	"fmt"
	"net/url"

	"github.com/monoes/mono-agent/internal/workflow"
)

// HubSpotNode implements the service.hubspot node type.
type HubSpotNode struct{}

func (n *HubSpotNode) Type() string { return "service.hubspot" }

// hubspotBaseURL is a var (not const) so tests can point it at an httptest server.
var hubspotBaseURL = "https://api.hubapi.com"

// hubspotObjectType maps operation prefixes to HubSpot CRM object type paths.
func hubspotObjectPath(objectType string) string {
	return hubspotBaseURL + "/crm/v3/objects/" + objectType
}

func (n *HubSpotNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	accessToken := strVal(config, "access_token")
	if accessToken == "" {
		return nil, fmt.Errorf("hubspot: access_token is required")
	}
	operation := strVal(config, "operation")
	limit := intVal(config, "limit")
	if limit == 0 {
		limit = 10
	}
	after := strVal(config, "after")
	objectID := strVal(config, "object_id")
	properties := mapVal(config, "properties")

	var items []workflow.Item

	switch operation {
	case "list_contacts":
		items_out, err := hubspotList(ctx, accessToken, "contacts", limit, after)
		if err != nil {
			return nil, err
		}
		items = items_out

	case "create_contact":
		data, err := hubspotCreate(ctx, accessToken, "contacts", properties)
		if err != nil {
			return nil, err
		}
		items = []workflow.Item{workflow.NewItem(data)}

	case "update_contact":
		if objectID == "" {
			return nil, fmt.Errorf("hubspot: object_id is required for update_contact")
		}
		data, err := hubspotUpdate(ctx, accessToken, "contacts", objectID, properties)
		if err != nil {
			return nil, err
		}
		items = []workflow.Item{workflow.NewItem(data)}

	case "get_contact":
		if objectID == "" {
			return nil, fmt.Errorf("hubspot: object_id is required for get_contact")
		}
		data, err := hubspotGet(ctx, accessToken, "contacts", objectID)
		if err != nil {
			return nil, err
		}
		items = []workflow.Item{workflow.NewItem(data)}

	case "list_deals":
		items_out, err := hubspotList(ctx, accessToken, "deals", limit, after)
		if err != nil {
			return nil, err
		}
		items = items_out

	case "create_deal":
		data, err := hubspotCreate(ctx, accessToken, "deals", properties)
		if err != nil {
			return nil, err
		}
		items = []workflow.Item{workflow.NewItem(data)}

	case "update_deal":
		if objectID == "" {
			return nil, fmt.Errorf("hubspot: object_id is required for update_deal")
		}
		data, err := hubspotUpdate(ctx, accessToken, "deals", objectID, properties)
		if err != nil {
			return nil, err
		}
		items = []workflow.Item{workflow.NewItem(data)}

	case "list_companies":
		items_out, err := hubspotList(ctx, accessToken, "companies", limit, after)
		if err != nil {
			return nil, err
		}
		items = items_out

	case "create_company":
		data, err := hubspotCreate(ctx, accessToken, "companies", properties)
		if err != nil {
			return nil, err
		}
		items = []workflow.Item{workflow.NewItem(data)}

	case "update_company":
		if objectID == "" {
			return nil, fmt.Errorf("hubspot: object_id is required for update_company")
		}
		data, err := hubspotUpdate(ctx, accessToken, "companies", objectID, properties)
		if err != nil {
			return nil, err
		}
		items = []workflow.Item{workflow.NewItem(data)}

	default:
		return nil, fmt.Errorf("hubspot: unknown operation %q", operation)
	}

	return []workflow.NodeOutput{{Handle: "main", Items: items}}, nil
}

// hubspotList fetches CRM objects and follows the paging.next.after cursor
// (up to maxListPages requests), returning them as Items.
func hubspotList(ctx context.Context, accessToken, objectType string, limit int, after string) ([]workflow.Item, error) {
	var items []workflow.Item
	truncated := false
	for page := 0; page < maxListPages; page++ {
		params := url.Values{"limit": {fmt.Sprintf("%d", limit)}}
		if after != "" {
			params.Set("after", after)
		}
		data, err := apiRequest(ctx, "GET", hubspotObjectPath(objectType)+"?"+params.Encode(), accessToken, nil)
		if err != nil {
			return nil, fmt.Errorf("hubspot list %s: %w", objectType, err)
		}
		results, _ := data["results"].([]interface{})
		for _, r := range results {
			if m, ok := r.(map[string]interface{}); ok {
				items = append(items, workflow.NewItem(m))
			}
		}
		paging, _ := data["paging"].(map[string]interface{})
		if paging == nil {
			break
		}
		next, _ := paging["next"].(map[string]interface{})
		if next == nil {
			break
		}
		nextAfter, _ := next["after"].(string)
		if nextAfter == "" {
			break
		}
		if page == maxListPages-1 {
			truncated = true
			break
		}
		after = nextAfter
	}
	if truncated {
		flagTruncated(items)
	}
	return items, nil
}

// hubspotGet fetches a single CRM object by ID.
func hubspotGet(ctx context.Context, accessToken, objectType, objectID string) (map[string]interface{}, error) {
	u := hubspotObjectPath(objectType) + "/" + url.PathEscape(objectID)
	data, err := apiRequest(ctx, "GET", u, accessToken, nil)
	if err != nil {
		return nil, fmt.Errorf("hubspot get %s/%s: %w", objectType, objectID, err)
	}
	return data, nil
}

// hubspotCreate creates a new CRM object with the given properties.
func hubspotCreate(ctx context.Context, accessToken, objectType string, properties map[string]interface{}) (map[string]interface{}, error) {
	if properties == nil {
		properties = map[string]interface{}{}
	}
	body := map[string]interface{}{"properties": properties}
	data, err := apiRequest(ctx, "POST", hubspotObjectPath(objectType), accessToken, body)
	if err != nil {
		return nil, fmt.Errorf("hubspot create %s: %w", objectType, err)
	}
	return data, nil
}

// hubspotUpdate updates a CRM object by ID.
func hubspotUpdate(ctx context.Context, accessToken, objectType, objectID string, properties map[string]interface{}) (map[string]interface{}, error) {
	if properties == nil {
		properties = map[string]interface{}{}
	}
	body := map[string]interface{}{"properties": properties}
	u := hubspotObjectPath(objectType) + "/" + url.PathEscape(objectID)
	data, err := apiRequest(ctx, "PATCH", u, accessToken, body)
	if err != nil {
		return nil, fmt.Errorf("hubspot update %s/%s: %w", objectType, objectID, err)
	}
	return data, nil
}
