# Action Definitions (data/actions/)

JSON action definitions, embedded at compile time via `data/embed.go` and
executed by the action engine. One file per action; the filename is the
action name. Each file's `description` field is the authoritative summary.

## Layout

```
data/actions/
├── gemini/         # in the default build
├── hackernews/     # requires -tags social
├── instagram/      # requires -tags social
├── linkedin/       # requires -tags social
├── producthunt/    # requires -tags social
├── tiktok/         # requires -tags social
└── x/              # requires -tags social
```

The social platform actions are only compiled in with
`go build -tags social ./cmd/monoagentcli` (see `docs/USAGE_POLICY.md`).
Gemini actions are part of the default build.

## Actions per platform

### gemini (default build)

- `chat_session` — send a prompt to a persistent Gemini session (text or image); resumes via `session_id`
- `chat_session_many` — send a list of prompts to one Gemini session for consistent style across answers
- `generate_image` — send a prompt to Gemini and download generated images (3-tier fallback)
- `generate_text` — send a prompt to Gemini and extract the text response (3-tier fallback)

### hackernews (social build)

- `get_post_metrics` — read the point score and comment count of a Hacker News item
- `list_comments` — list the top-level comments on a Hacker News item
- `reply_to_comment` — reply to a Hacker News item or comment thread
- `submit_post` — submit a new post (link or text/Show HN) to Hacker News

### instagram (social build)

- `auto_reply_dms` — reply to messages in the Instagram inbox (3-tier fallback)
- `comment_on_posts` — comment on posts from a list of post URLs
- `engage_user_posts` — like or comment on a user's recent posts (3-tier fallback)
- `engage_with_posts` — like or comment on posts using a 3-tier fallback pattern
- `export_followers` — fetch the followers or following list of a profile
- `extract_post_data` — scrape data from posts (3-tier fallback)
- `find_by_keyword` — search for posts and extract profile info from each
- `follow_users` — follow users from a list of profile URLs
- `like_comments_on_posts` — like comments on posts (3-tier fallback)
- `like_posts` — like posts from a list of post URLs
- `list_post_comments` — list comments on a post
- `list_user_posts` — list posts from a profile grid
- `publish_post` — publish content (post/story)
- `reply_to_comments` — reply to comments on posts
- `scrape_profile_info` — extract profile information (3-tier fallback)
- `send_dms` — send direct messages to a list of recipients
- `unfollow_users` — unfollow users from a list of profile URLs
- `watch_stories` — view stories from a list of profile URLs

### linkedin (social build)

- `auto_reply_dms` — reply to messages in the LinkedIn inbox
- `comment_on_posts` — comment on posts or reply to a specific comment
- `engage_with_posts` — like or comment on posts
- `export_followers` — fetch the followers or following list of a profile
- `find_by_keyword` — search for people using keywords
- `like_comments` — like comments on a post by comment URN ID
- `like_posts` — react to posts (Like, Celebrate, Support, Love, Insightful, Funny)
- `list_post_comments` — list comments (and optionally replies) on posts
- `list_user_posts` — list posts from a profile or company page
- `publish_post` — publish content (post)
- `scrape_profile_info` — extract profile information
- `send_dms` — send direct messages to a list of recipients

### producthunt (social build)

- `comment_on_launch` — post a comment on a launch page
- `get_launch_metrics` — read the upvote and comment count of a launch page
- `list_comments` — list the visible comments on a launch page

### tiktok (social build)

- `auto_reply_dms` — reply to messages in the TikTok inbox
- `comment_on_video` — post a comment on videos from a list of video URLs
- `duet_video` — open the duet creation flow for videos
- `engage_with_posts` — like or comment on videos
- `export_followers` — fetch the followers or following list of a profile
- `find_by_keyword` — search for videos and users using keywords
- `follow_user` — follow users from a list of profile URLs
- `like_comment` — like a specific comment on videos
- `like_video` — like videos from a list of video URLs
- `list_user_videos` — collect video URLs from profile video grids
- `list_video_comments` — list comments on videos
- `publish_post` — publish content (video)
- `scrape_profile_info` — extract profile information
- `send_dms` — send direct messages to a list of recipients
- `share_video` — copy the share link for videos and return the URL
- `stitch_video` — open the stitch creation flow for videos

### x (social build)

- `auto_reply_dms` — reply to messages in the X inbox
- `engage_with_posts` — like or comment on posts
- `export_followers` — fetch the followers or following list of a profile
- `find_by_keyword` — search for tweets and users using keywords
- `publish_post` — publish content (tweet)
- `scrape_profile_info` — extract profile information
- `send_dms` — send direct messages to a list of recipients

## Adding a new action

1. Create `data/actions/<platform>/<action_name>.json` (copy an existing
   file as a template — the schema lives in the JSON files themselves)
2. If using `call_bot_method`, implement the Go method in
   `internal/bot/<platform>/bot.go`
3. Run `go build ./...` (or `go build -tags social ./...`) to verify — the
   embedded FS picks up new files automatically
