// SPDX-License-Identifier: GPL-3.0-or-later

package netstack

import "context"

type tagsKeyType struct{}

var tagsKey tagsKeyType

// ContextWithTags returns a new context carrying the given tags.
func ContextWithTags(ctx context.Context, tags []string) context.Context {
	return context.WithValue(ctx, tagsKey, tags)
}

// TagsFromContext returns the tags from the context, or nil.
func TagsFromContext(ctx context.Context) []string {
	tags, _ := ctx.Value(tagsKey).([]string)
	return tags
}
