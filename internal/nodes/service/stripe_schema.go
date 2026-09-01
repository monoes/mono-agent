package service

// StripeNodeSchema documents the config keys StripeNode.Execute reads out of
// its map[string]interface{} config — see internal/nodes/control/set_schema.go's
// doc comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// Auth (secret_key / legacy api_key) is resolved by the credential layer
// from the saved connection and is not exposed as a schema field, matching
// the hand-written schema this replaces.
//
// "limit" defaults to 10 in Execute (`if limit == 0 { limit = 10 }`), not
// 20 as the hand-written schemas/service.stripe.json this replaces
// claimed.
//
// credential_platform: stripe
type StripeNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Stripe Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=list_customers|create_customer|get_customer|list_charges|create_charge|list_subscriptions|create_subscription|cancel_subscription|list_products|create_payment_intent,default=list_customers"`

	CustomerID string `json:"customer_id" schema:"label=Customer ID,type=text,depends_on_key=operation,depends_on_values=get_customer|list_charges|create_charge|list_subscriptions|create_subscription|create_payment_intent"`

	Email string `json:"email" schema:"label=Email,type=text,depends_on_key=operation,depends_on_values=create_customer"`

	Name string `json:"name" schema:"label=Name,type=text,depends_on_key=operation,depends_on_values=create_customer"`

	Amount float64 `json:"amount" schema:"label=Amount (cents),type=number,depends_on_key=operation,depends_on_values=create_charge|create_payment_intent"`

	Currency string `json:"currency" schema:"label=Currency,type=text,default=usd,depends_on_key=operation,depends_on_values=create_charge|create_payment_intent"`

	Source string `json:"source" schema:"label=Source Token,type=text,depends_on_key=operation,depends_on_values=create_charge"`

	PriceID string `json:"price_id" schema:"label=Price ID,type=text,placeholder=price_...,depends_on_key=operation,depends_on_values=create_subscription"`

	SubscriptionID string `json:"subscription_id" schema:"label=Subscription ID,type=text,depends_on_key=operation,depends_on_values=cancel_subscription"`

	Limit float64 `json:"limit" schema:"label=Max Results,type=number,default=10"`
}
