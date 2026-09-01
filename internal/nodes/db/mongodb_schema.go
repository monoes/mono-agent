package dbnodes

// credential_platform: mongodb
//
// MongoDBNodeSchema documents the config keys MongoDBNode.Execute reads out
// of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// This matches the hand-written schemas/db.mongodb.json field-for-field.
type MongoDBNodeSchema struct {
	ConnectionString string `json:"connection_string" schema:"label=MongoDB Connection String,type=password,required,placeholder=mongodb://user:pass@host:27017/db"`

	Database string `json:"database" schema:"label=Database,type=text,required"`

	Collection string `json:"collection" schema:"label=Collection,type=text,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=find|insert_one|insert_many|update_one|update_many|delete_one|delete_many|aggregate,default=find"`

	Filter string `json:"filter" schema:"label=Filter (JSON),type=textarea,rows=3,placeholder={ \"status\": \"active\" }"`

	Update string `json:"update" schema:"label=Update (JSON),type=textarea,rows=3,depends_on_key=operation,depends_on_values=update_one|update_many"`

	Document string `json:"document" schema:"label=Document (JSON),type=textarea,rows=3,depends_on_key=operation,depends_on_values=insert_one|insert_many"`

	Pipeline string `json:"pipeline" schema:"label=Aggregation Pipeline (JSON array),type=textarea,rows=3,depends_on_key=operation,depends_on_values=aggregate"`

	Limit float64 `json:"limit" schema:"label=Limit,type=number,depends_on_key=operation,depends_on_values=find"`
}
