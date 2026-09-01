package dbnodes

// credential_platform: mysql
//
// MySQLNodeSchema documents the config keys MySQLNode.Execute reads out of
// its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// The hand-written schemas/db.mysql.json this replaces exposed only "query"
// (marked required) and "params" — but MySQLNode.Execute has always
// supported a second mode: when "query" is empty and "table" is set, it
// builds INSERT/UPDATE/DELETE/SELECT statements from "operation", "table",
// "data", and "where" (see buildMySQLQuery). That whole table-builder mode
// had no schema fields at all, so it was unreachable from the UI. Fixed
// here by adding operation/table/data/where, and by dropping "required"
// from "query" since a table-mode config can leave it blank (Execute itself
// still errors if neither "query" nor "table" is set — the schema can't
// express that either/or requirement, so this documents it via help text
// instead).
type MySQLNodeSchema struct {
	ConnectionString string `json:"connection_string" schema:"label=Connection String,type=password,required,placeholder=user:pass@tcp(host:3306)/db"`

	Operation string `json:"operation" schema:"label=Operation,type=select,options=select|insert|update|delete|execute,default=select,help=Only used to build a query from Table/Data/Where when SQL Query is blank. Ignored otherwise (the query's own verb governs execution)."`

	Query string `json:"query" schema:"label=SQL Query,type=code,placeholder=SELECT * FROM users WHERE active = ?,help=Either this or Table must be set. Takes precedence over Table/Data/Where when both are given."`

	Params string `json:"params" schema:"label=Query Parameters (JSON array),type=textarea,rows=3,placeholder=[1]"`

	Table string `json:"table" schema:"label=Table,type=text,help=Used with Operation/Data/Where to build a query when SQL Query is left blank."`

	Data string `json:"data" schema:"label=Row Data (JSON object),type=textarea,rows=3,placeholder={ \"name\": \"Ada\" },help=Column/value pairs for insert or update， used only in table-builder mode.,depends_on_key=operation,depends_on_values=insert|update"`

	Where string `json:"where" schema:"label=Where Clause,type=text,placeholder=id = ?,help=Raw SQL boolean expression (no leading WHERE). Required for update/delete in table-builder mode.,depends_on_key=operation,depends_on_values=select|update|delete"`
}
