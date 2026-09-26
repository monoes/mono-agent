// Fixture CRM for the automation e2e scripts: a login form (password field),
// a new-contact form that saves with fetch and shows a toast, a contacts list,
// and an upload page. V2=1 renames the name field so its recorded selectors
// miss (for rerecord / doctor checks). GET /__state returns the contacts.
// Every request is appended to $LOG; the login password is never logged, only
// whether it matched.
import http from "node:http";
import { appendFileSync } from "node:fs";
const PORT = Number(process.env.PORT || 18765);
const LOG = process.env.LOG || "requests.log";
const USER = "alice", PASS = "S3cr3t-Pa55word!";
const contacts = [
  { name: "Ada Lovelace", email: "ada@example.com" },
  { name: "Grace Hopper", email: "grace@example.com" },
  { name: "Alan Turing", email: "alan@example.com" },
  { name: "Edsger Dijkstra", email: "edsger@example.com" },
  { name: "Barbara Liskov", email: "barbara@example.com" },
];
const sessions = new Set();
const page = (title, body) => `<!doctype html><html><head><meta charset="utf-8"><title>${title}</title>
<style>body{font-family:sans-serif;margin:2em}.toast{position:fixed;top:1em;right:1em;background:#2a2;color:#fff;padding:.6em 1em;border-radius:6px}
nav a{margin-right:1em} label{display:block;margin:.5em 0} .contact{padding:.3em;border-bottom:1px solid #ddd}</style></head>
<body><nav><a href="/contacts" id="nav-contacts">Contacts</a><a href="/contacts/new" id="nav-new">New contact</a></nav>${body}</body></html>`;
const loginPage = (err) => page("Sign in — E2E CRM", `<h1>Sign in</h1>${err ? `<p class="error">${err}</p>` : ""}
<form method="post" action="/login" id="login-form">
<label>Username <input name="username" id="username" autocomplete="username"></label>
<label>Password <input type="password" name="password" id="password" autocomplete="current-password"></label>
<button type="submit" id="login-btn">Sign in</button></form>`);
const newPage = () => page("New contact — E2E CRM", `<h1>New contact</h1>
<form id="contact-form">
<label>${process.env.V2 ? "Contact person" : "Full name"} <input name="name" id="${process.env.V2 ? "full-name-v2" : "contact-name"}" placeholder="${process.env.V2 ? "Contact person" : "Full name"}"></label>
<label>Email <input name="email" id="contact-email" type="email" placeholder="Email"></label>
<label>Notes <textarea name="notes" id="contact-notes"></textarea></label>
<button type="submit" id="save-contact" data-testid="save-contact">Save</button></form>
<script>
document.getElementById("contact-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const r = await fetch("/api/contacts", {method:"POST", headers:{"content-type":"application/json"}, body: JSON.stringify(Object.fromEntries(fd))});
  const t = document.createElement("div"); t.className = "toast"; t.id = "toast"; t.setAttribute("role","status");
  t.textContent = r.ok ? "Contact saved" : "Save failed"; document.body.appendChild(t);
  if (r.ok) e.target.reset();
});
</script>`);
const listPage = () => page("Contacts — E2E CRM", `<h1>Contacts</h1><ul id="contact-list">${contacts
  .map((c, i) => `<li class="contact" data-id="${i + 1}"><span class="contact-name">${c.name}</span> <a class="contact-email" href="mailto:${c.email}">${c.email}</a></li>`)
  .join("")}</ul>`);
const authed = (req) => (req.headers.cookie || "").split(/;\s*/).some((c) => sessions.has(c.replace(/^sid=/, "")));
const body = (req) => new Promise((r) => { let b = ""; req.on("data", (d) => (b += d)); req.on("end", () => r(b)); });
http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://x");
  const b = req.method === "POST" ? await body(req) : "";
  appendFileSync(LOG, JSON.stringify({ t: new Date().toISOString(), host: req.headers.host, m: req.method, p: url.pathname, body: url.pathname === "/login" ? (b ? `pwOK=${new URLSearchParams(b).get("password") === PASS} user=${new URLSearchParams(b).get("username")}` : "") : b }) + "\n");
  const html = (s, h) => { res.writeHead(s, { "content-type": "text/html; charset=utf-8" }); res.end(h); };
  if (url.pathname === "/" ) { res.writeHead(302, { location: "/login" }); return res.end(); }
  if (url.pathname === "/login" && req.method === "GET") return html(200, loginPage());
  if (url.pathname === "/login" && req.method === "POST") {
    const f = new URLSearchParams(b);
    if (f.get("username") === USER && f.get("password") === PASS) {
      const sid = Math.random().toString(36).slice(2); sessions.add(sid);
      res.writeHead(302, { location: "/contacts/new", "set-cookie": `sid=${sid}; Path=/; HttpOnly` }); return res.end();
    }
    return html(401, loginPage("Wrong username or password"));
  }
  if (url.pathname === "/upload") return html(200, page("Upload", `<h1>Upload</h1><input type="file" id="file-input" name="f"><p id="chosen"></p><script>document.getElementById("file-input").addEventListener("change",e=>{const f=e.target.files[0];document.getElementById("chosen").textContent=f?f.name+":"+f.size:"";});</script>`));
  if (url.pathname === "/__state") { res.writeHead(200, { "content-type": "application/json" }); return res.end(JSON.stringify({ contacts })); }
  if (!authed(req)) { res.writeHead(302, { location: "/login" }); return res.end(); }
  if (url.pathname === "/contacts/new") return html(200, newPage());
  if (url.pathname === "/contacts") return html(200, listPage());
  if (url.pathname === "/api/contacts" && req.method === "POST") {
    const c = JSON.parse(b || "{}"); if (!c.name) { res.writeHead(400); return res.end("{}"); }
    contacts.push({ name: c.name, email: c.email || "" });
    res.writeHead(201, { "content-type": "application/json" }); return res.end(JSON.stringify({ ok: true, id: contacts.length }));
  }
  html(404, page("Not found", "<h1>404</h1>"));
}).listen(PORT, "127.0.0.1", () => console.log("fixture on", PORT));
