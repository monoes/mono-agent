package dbnodes

// credential_platform: redis
//
// RedisNodeSchema documents the config keys RedisNode.Execute reads out of
// its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// This matches the hand-written schemas/db.redis.json field-for-field.
// Note: "key" is marked required here (matching the hand-written schema),
// but Execute only enforces that for most operations — for "keys" it is
// actually optional and defaults to the glob pattern "*" when blank. The
// hand-written schema already carried this same imprecision; a per-operation
// conditional "required" isn't expressible in the tag grammar, so it's
// called out here rather than silently changed.
type RedisNodeSchema struct {
	ConnectionString string `json:"connection_string" schema:"label=Redis Connection String,type=password,required,placeholder=redis://:password@localhost:6379/0"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=get|set|del|exists|expire|hget|hset|hgetall|lpush|lrange|keys|incr,default=get"`

	Key string `json:"key" schema:"label=Key / Pattern,type=text,required,help=For 'keys'， this is a glob pattern (default '*')."`

	Value string `json:"value" schema:"label=Value,type=text,depends_on_key=operation,depends_on_values=set|hset|lpush"`

	Field string `json:"field" schema:"label=Hash Field,type=text,depends_on_key=operation,depends_on_values=hget|hset"`

	TTLSeconds float64 `json:"ttl_seconds" schema:"label=TTL (seconds),type=number,depends_on_key=operation,depends_on_values=set|expire"`

	Start float64 `json:"start" schema:"label=Start Index,type=number,default=0,depends_on_key=operation,depends_on_values=lrange"`

	Stop float64 `json:"stop" schema:"label=Stop Index,type=number,default=-1,depends_on_key=operation,depends_on_values=lrange"`
}
