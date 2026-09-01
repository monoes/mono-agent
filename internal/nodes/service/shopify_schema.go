package service

// ShopifyNodeSchema documents the config keys ShopifyNode.Execute reads out
// of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// Unlike the other nodes in this package, the hand-written
// schemas/service.shopify.json this replaces has no "credential_id" field
// — it exposes "shop_domain" and "access_token" directly as required text/
// password fields, even though "credential_platform" is set to "shopify".
// That is preserved as-is here rather than switched to a credential_picker,
// since Execute genuinely reads both keys directly
// (strVal(config, "shop_domain"), strVal(config, "access_token")).
//
// Several real gaps between Execute and the hand-written JSON were found
// while writing this struct, all fixed here rather than silently carried
// forward:
//
//   - "get_customer", "create_product", and "update_product" are operations
//     Execute has supported since it was written, but the hand-written
//     schema's operation options list never included them.
//   - "product_id", "title", "vendor", "product_type", "order_id", and
//     "customer_id" are config keys Execute reads for those (and existing)
//     operations, but had no schema field at all.
//   - "limit" defaults to 50 in Execute (`if limit == 0 { limit = 50 }`),
//     not 20 as the hand-written schema's default claimed.
//   - "status" is also read for update_order (sets financial_status), not
//     just list_orders as the old schema's depends_on implied. Note the
//     select's any|open|closed|cancelled options are the list_orders
//     fulfillment-status filter values, not valid financial_status values
//     (e.g. "paid", "refunded", "voided") — reusing the same field/options
//     for update_order is carried forward from the original config-key
//     overlap in Execute, not verified against Shopify's financial_status
//     enum.
//
// credential_platform: shopify
type ShopifyNodeSchema struct {
	ShopDomain string `json:"shop_domain" schema:"label=Shop Domain,type=text,required,placeholder=mystore.myshopify.com"`

	AccessToken string `json:"access_token" schema:"label=Access Token,type=password,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=list_products|get_product|create_product|update_product|list_orders|get_order|update_order|list_customers|get_customer,default=list_orders"`

	Limit float64 `json:"limit" schema:"label=Max Results,type=number,default=50"`

	ProductID string `json:"product_id" schema:"label=Product ID,type=text,depends_on_key=operation,depends_on_values=get_product|update_product"`

	Title string `json:"title" schema:"label=Title,type=text,depends_on_key=operation,depends_on_values=create_product|update_product"`

	Vendor string `json:"vendor" schema:"label=Vendor,type=text,depends_on_key=operation,depends_on_values=create_product|update_product"`

	ProductType string `json:"product_type" schema:"label=Product Type,type=text,depends_on_key=operation,depends_on_values=create_product|update_product"`

	OrderID string `json:"order_id" schema:"label=Order ID,type=text,depends_on_key=operation,depends_on_values=get_order|update_order"`

	Status string `json:"status" schema:"label=Order Status Filter,type=select,options=any|open|closed|cancelled,default=open,depends_on_key=operation,depends_on_values=list_orders|update_order"`

	CustomerID string `json:"customer_id" schema:"label=Customer ID,type=text,depends_on_key=operation,depends_on_values=get_customer"`
}
