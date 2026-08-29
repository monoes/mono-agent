# Usage Policy

Plain-English rules for what Mono Agent is for, what we ship, and where the
responsibility sits. This isn't legalese — it's the honest version.

## What Mono Agent is for

Mono Agent automates your own accounts, inbox, and tools — it is not a growth,
engagement, or outreach service for accounts you don't own.

It's a local-first workflow engine: you build automations that run on your
machine, against services and accounts that are yours, at a pace you control.

## Your own accounts only

You may only connect accounts you own or operate. If you want to run
something on behalf of another person or an employer, you need their explicit
consent first. Automating an account you don't have rights to is a misuse of
this tool, full stop.

## Human approval is available on any step

The `core.human_in_loop` node pauses a workflow and puts each pending item in
a durable approval queue (stored in SQLite — it survives restarts). A person
can review the data, edit any field, and approve or reject each item.
Rejection stops that branch; approval resumes it with the edited data.
Approvals can also be handled from the CLI (`monoagentcli hil list` /
`hil approve` / `hil reject`) or by an AI agent acting on your instruction.

**We strongly recommend putting a human-in-the-loop node in front of anything
that messages another person** — DMs, comments, emails, replies. This is a
recommendation and your decision, not something we force in code: it's your
machine and your account. But a 10-second review has saved better people than
us from worse mistakes.

## Rate limits that ship by default

The social action definitions in `data/actions/` ship with conservative
default caps and delays. These exist to protect **your** account from
triggering platform spam defenses. The defaults (which you can change):

### How these numbers are enforced

- **Per-session caps are enforced by the executor.** The loop that drives
  each of these actions stops when the cap is hit and records where it
  stopped, so the next run resumes at the boundary instead of redoing (and
  re-sending) capped items. Enforced today: `send_dms` (30),
  `comment_on_posts` (20), `like_posts` (50), `like_comments_on_posts`
  (100), `follow_users` (50), `unfollow_users` (50) — all on Instagram.
- **Daily caps are advisory, not yet enforced.** The `maxFollowsPerDay`
  (150), `maxUnfollowsPerDay` (150), and `maxRepliesPerDay` (100) inputs
  exist on their actions, but nothing counts usage across days yet — no
  daily counter is persisted. Treat them as your own budget until daily
  enforcement lands.
- **Delays are enforced** as wait steps between items; the table below
  lists the default values exactly as they ship in the JSON.

### Instagram

| Action | Per-session cap | Delay | Daily cap |
|---|---|---|---|
| `send_dms` | 30 messages | 15 s between messages | — |
| `comment_on_posts` | 20 comments | 10 s between comments | — |
| `like_posts` | 50 likes | 3 s between likes | — |
| `like_comments_on_posts` | 100 likes | 2 s between likes | — |
| `follow_users` | 50 follows | 5 s between follows | 150 *(advisory)* |
| `unfollow_users` | 50 unfollows | 5 s between unfollows | 150 *(advisory)* |
| `auto_reply_dms` | — | 5 s before each reply | 100 *(advisory)* |
| `engage_with_posts` | — | 30 s between engagement sessions | — |
| `engage_user_posts` | — | 5 s between posts | — |
| `reply_to_comments` | — | 10 s between replies | — |
| `watch_stories` | — | 3 s between stories / 5 s between profiles | — |
| `extract_post_data` | — | 2 s between posts | — |
| `list_post_comments` / `list_user_posts` | — | 3 s between posts | — |

Per-session caps in this table are executor-enforced (see above); rows
showing "—" ship without a session cap.

### LinkedIn

| Action | Per-session cap | Delay | Daily cap |
|---|---|---|---|
| `like_posts` | — | 3 s between likes | — |
| `like_comments` | — | 2 s between likes | — |
| `comment_on_posts` | — | 5 s between comments | — |
| `list_post_comments` / `list_user_posts` | — | 3 s between posts | — |

### TikTok

| Action | Per-session cap | Delay | Daily cap |
|---|---|---|---|
| `comment_on_video` | — | 5 s between comments | — |
| `like_video` | — | 3 s between likes | — |
| `follow_user` | — | 5 s between follows | — |
| `duet_video` / `stitch_video` | — | 5 s between videos | — |
| `share_video` | — | 3 s between videos | — |
| `list_user_videos` / `list_video_comments` | — | 3 s between items | — |

### X

X actions ship pacing delays inside their steps but **no per-session or
daily caps** — if you automate activity on X, set your own limits and keep
them modest.

Overriding any of these defaults is at your own risk, against the platform's
terms and enforcement systems. The caps are there to keep your account alive;
loosening them is your call and your consequence.

**Note on message variants:** earlier versions supported rotating multiple
variants of the same message/comment (spinning slightly different wording per
recipient). That feature was **removed from the codebase as a matter of
policy** — sending the same message with randomized wording is a spam
technique, and we won't ship tooling for it.

## Platform terms, stated plainly

The terms of service of most platforms restrict automated access to accounts
outside their official APIs. That's the reality; we won't pretend otherwise.

Our approach:

- **Where an official API exists, we prefer it.** Mono Agent ships
  official-API nodes for GitHub, Google Sheets, Gmail, Slack, Linear, Notion,
  Jira, Stripe, Airtable, HubSpot, Salesforce, Telegram, Discord, Reddit,
  Bluesky, Mastodon, and more.
- **Where no practical API exists** (e.g. scheduling a post to some networks),
  browser automation is provided for publishing to and reading **your own
  logged-in accounts**. You accept the platform-ToS risk that comes with it.
- We ship no stealth or anti-detection browser tooling. Input pacing (delays
  and typing jitter) exists to keep your own logged-in session stable across
  long runs — not to defeat platform detection.

## What we will not build

Some categories of "automation" are just abuse with extra steps. We won't
build them, ship them, or accept contributions adding them:

- **Engagement farming** — inflating likes, comments, views, or engagement on
  any account, yours or others'.
- **Vote or review manipulation** — this project ships no
  vote-manipulation or astroturfing tooling, and the browser-automation
  layer contains no vote or review actions at all. The Reddit service node
  does expose the official Reddit API (OAuth) endpoints — including votes —
  used against your own account under Reddit's API terms; that is ordinary
  API access to your own account, not manipulation tooling, and it stays
  subject to Reddit's rules.
- **Follower farming** — bulk follow/unfollow churn to game follower counts.
- **Mass unsolicited outreach** — spam DMs, bulk cold messages to people who
  never asked for contact, or any tooling whose main purpose is dodging the
  word "no."

## Social nodes are opt-in at build time

Social platform automation (Instagram, LinkedIn, X, TikTok, Hacker News,
Product Hunt) is **not in the default binary**. It only exists if you
deliberately compile it in:

```bash
go build -tags social ./cmd/monoagentcli
```

A default build simply doesn't contain those node types. That keeps the
default install a clean, general automation tool.

## No affiliation

Mono Agent is an independent, unofficial, MIT-licensed open-source project.
It is not affiliated with, authorized by, maintained by, sponsored by, or
endorsed by Meta (Instagram, Facebook), LinkedIn, X (Twitter), TikTok,
Google, or any other platform or company whose services it can automate.
All product names, logos, and brands are the property of their respective
owners. The maintainers are not responsible for how you use this software;
use it at your own risk and in accordance with the terms of every platform
you connect it to.
