//go:build !nosocial

package tiktok

import (
	"context"
	"strconv"
	"strings"

	botpkg "github.com/monoes/mono-agent/internal/bot"
)

// methodFunc is what call_bot_method dispatches to: args[0] is the page,
// the rest are the step's resolved args.
type methodFunc = func(ctx context.Context, args ...interface{}) (interface{}, error)

// atoiDefault parses a count argument, falling back to def.
func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// truthy parses an optional boolean argument.
func truthy(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return def
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// GetMethodByName returns the call_bot_method entry point for name. Every
// method takes the page first (prepended by the executor), then:
//
//	list_user_videos    (profileURL, maxCount?)
//	list_video_comments (videoURL, maxCount?)
//	get_profile_data    (profileURL?)          — current page when empty
//	search_videos       (keyword, maxCount?)
//	list_followers      (profileURL, sourceType?, maxCount?)
//	list_conversations  (maxCount?, unreadOnly?)
//	like_video          (videoURL)
//	like_comment        (videoURL, commentID?, username?, text?)
//	comment_on_video    (videoURL, commentText)
//	follow_user         (profileURL)
//	send_dm             (profileURL|username, message)
//	reply_to_conversation (conversationName, message)
//	publish_uploaded_video (caption?)
//	share_video / stitch_video / duet_video (videoURL) — open TikTok's UI only
func (b *TikTokBot) GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool) {
	var fn methodFunc
	switch name {
	case "list_user_videos":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 2, "profileURL", "maxCount?")
			if err != nil {
				return nil, err
			}
			return b.ListUserVideos(ctx, page, a[0], atoiDefault(a[1], 20))
		}
	case "list_video_comments":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 2, "videoURL", "maxCount?")
			if err != nil {
				return nil, err
			}
			return b.ListVideoComments(ctx, page, a[0], atoiDefault(a[1], 50))
		}
	case "get_profile_data":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 1, "profileURL?")
			if err != nil {
				return nil, err
			}
			return b.getProfileData(ctx, page, a[0])
		}
	case "search_videos":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 2, "keyword", "maxCount?")
			if err != nil {
				return nil, err
			}
			return b.SearchVideos(ctx, page, a[0], atoiDefault(a[1], 20))
		}
	case "list_followers":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 3, "profileURL", "sourceType?", "maxCount?")
			if err != nil {
				return nil, err
			}
			return b.ListFollowers(ctx, page, a[0], a[1], atoiDefault(a[2], 50))
		}
	case "list_conversations":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 2, "maxCount?", "unreadOnly?")
			if err != nil {
				return nil, err
			}
			return b.ListConversations(ctx, page, atoiDefault(a[0], 20), truthy(a[1], true))
		}
	case "like_video":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 1, "videoURL")
			if err != nil {
				return nil, err
			}
			return b.LikeVideo(ctx, page, a[0])
		}
	case "like_comment":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 4, "videoURL", "commentID?", "username?", "text?")
			if err != nil {
				return nil, err
			}
			return b.LikeComment(ctx, page, a[0], a[1], a[2], a[3])
		}
	case "comment_on_video":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 2, "videoURL", "commentText")
			if err != nil {
				return nil, err
			}
			return b.CommentOnVideo(ctx, page, a[0], a[1])
		}
	case "follow_user":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 1, "profileURL")
			if err != nil {
				return nil, err
			}
			return b.FollowUser(ctx, page, a[0])
		}
	case "send_dm":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 2, "profileURL", "message")
			if err != nil {
				return nil, err
			}
			return b.SendDM(ctx, page, a[0], a[1])
		}
	case "reply_to_conversation":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 2, "conversationName", "message")
			if err != nil {
				return nil, err
			}
			return b.ReplyToConversation(ctx, page, a[0], a[1])
		}
	case "publish_uploaded_video":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 1, "caption?")
			if err != nil {
				return nil, err
			}
			return b.PublishUploadedVideo(ctx, page, a[0])
		}
	case "stitch_video":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 1, "videoURL")
			if err != nil {
				return nil, err
			}
			return b.StitchVideo(ctx, page, a[0])
		}
	case "duet_video":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 1, "videoURL")
			if err != nil {
				return nil, err
			}
			return b.DuetVideo(ctx, page, a[0])
		}
	case "share_video":
		fn = func(ctx context.Context, args ...interface{}) (interface{}, error) {
			page, a, err := botpkg.Args(args, 1, "videoURL")
			if err != nil {
				return nil, err
			}
			return b.ShareVideo(ctx, page, a[0])
		}
	default:
		return nil, false
	}
	return fn, true
}
