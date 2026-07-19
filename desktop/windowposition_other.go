//go:build !windows

package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func setNativeWindowPosition(ctx context.Context, x, y int) {
	runtime.WindowSetPosition(ctx, x, y)
}
