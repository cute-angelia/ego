package etrace_test

import (
	"context"
	"fmt"
	"time"

	"github.com/gotomicro/ego/core/etrace"
)

func ExampleTraceFunc() {
	// 1. 从配置文件中初始化
	process1 := func(ctx context.Context) {
		span, ctx := etrace.StartSpanFromContext(ctx, "process1")
		defer span.Finish()

		// todo something
		fmt.Println("error", ctx.Err())
		time.Sleep(time.Second)
	}

	process2 := func(ctx context.Context) {
		span, ctx := etrace.StartSpanFromContext(ctx, "process2")
		defer span.Finish()
		process1(ctx)
	}

	process3 := func(ctx context.Context) {
		span, ctx := etrace.StartSpanFromContext(ctx, "process3")
		defer span.Finish()
		process2(ctx)
	}

	process3(context.Background())
}
