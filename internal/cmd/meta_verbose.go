package cmd

import (
	"context"
	"nosygit/internal/config"
	"nosygit/internal/events"
)

func emitVerboseMetaInfo(ctx context.Context, component string, code string, message string, details map[string]interface{}) {
	ctxValue, ok := ctx.Value("ctx").(config.ContextStruct)
	if !ok || !ctxValue.Verbose {
		return
	}
	events.EmitMetaInfo(ctx, component, code, message, details)
}
