package httpnodes

// credential_platform: ssh
//
// SSHNodeSchema documents the config keys SSHNode.Execute reads out of its
// map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// Two fields have no entry in the hand-written schemas/http.ssh.json this
// replaces, both of which Execute has always read:
//
//   - "timeout_seconds" (default 30 in code) bounds how long the remote
//     command may run before the node cancels it and returns an error.
//   - "known_hosts" switches host key verification from
//     ssh.InsecureIgnoreHostKey() (the fallback, which logs a WARNING) to a
//     real known_hosts file check. Security-relevant and previously
//     unreachable from the UI.
type SSHNodeSchema struct {
	Host string `json:"host" schema:"label=SSH Host,type=text,required"`

	Port float64 `json:"port" schema:"label=Port,type=number,default=22"`

	Username string `json:"username" schema:"label=Username,type=text,required"`

	Password string `json:"password" schema:"label=Password,type=password"`

	PrivateKey string `json:"private_key" schema:"label=Private Key (PEM),type=textarea,rows=6"`

	Passphrase string `json:"passphrase" schema:"label=Key Passphrase,type=password"`

	KnownHosts string `json:"known_hosts" schema:"label=Known Hosts File,type=text,help=Path to a known_hosts file used to verify the server's host key. If left blank， host key verification is disabled (insecure) and a warning is logged."`

	Command string `json:"command" schema:"label=Command,type=text,required,placeholder=ls -la /var/log"`

	TimeoutSeconds float64 `json:"timeout_seconds" schema:"label=Timeout (seconds),type=number,default=30,help=Command is cancelled if it runs longer than this."`
}
