package flamingo_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"

	"flamingo.me/dingo"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"flamingo.me/flamingo/v3"
	framework "flamingo.me/flamingo/v3/framework/flamingo"
	"flamingo.me/flamingo/v3/framework/flamingo/mocks"
)

type NotifyFunc func(ctx context.Context, event framework.Event)

func (nf NotifyFunc) Notify(ctx context.Context, event framework.Event) {
	nf(ctx, event)
}

func buildSignalSender(t *testing.T) func() {
	t.Helper()

	return func() {
		pid := os.Getpid()
		process, err := os.FindProcess(pid)
		require.NoError(t, err)

		err = process.Signal(os.Interrupt)
		require.NoError(t, err)
	}
}

func TestAppRunServeAndShutdown(t *testing.T) { //nolint:paralleltest // because of dingo.Singleton
	tests := []struct {
		name            string
		args            string
		onServerStartup func()
		logger          framework.Logger
	}{
		{
			name:            "serve command interrupted and HTTP server is shutdown",
			args:            "serve",
			onServerStartup: sync.OnceFunc(buildSignalSender(t)), // sends os.Interrupt, triggers graceful shutdown
			logger: func() framework.Logger {
				// No higher level than INFO calls are expected
				logger := mocks.NewLogger(t)
				logger.EXPECT().WithField(mock.Anything, mock.Anything).Maybe().Return(logger)
				logger.EXPECT().Debug(mock.Anything).Maybe()
				logger.EXPECT().Info(mock.Anything).Maybe()
				logger.EXPECT().Info(mock.Anything, mock.Anything).Maybe()

				return logger
			}(),
		},
	}

	for _, tt := range tests { //nolint:paralleltest // because of dingo.Singleton
		t.Run(tt.name, func(t *testing.T) {
			modules := []dingo.Module{
				dingo.ModuleFunc(func(injector *dingo.Injector) {
					framework.BindEventSubscriber(injector).ToInstance(NotifyFunc(func(ctx context.Context, event framework.Event) {
						if ev, ok := event.(*framework.ServerStartEvent); ok {
							if _, err := net.Dial("tcp", fmt.Sprintf(":%s", ev.Port)); err != nil {
								t.Fatalf("failed to connect to server")
							}

							tt.onServerStartup()
						}
					}))
				})}

			dingo.Singleton = dingo.NewSingletonScope()
			dingo.ChildSingleton = dingo.NewChildSingletonScope()

			app, err := flamingo.NewApplication(modules,
				flamingo.WithArgs(tt.args),
				flamingo.WithCustomLogger(dingo.ModuleFunc(func(injector *dingo.Injector) {
					injector.Bind(new(framework.Logger)).ToInstance(tt.logger)
				})),
			)
			require.NoError(t, err)

			err = app.Run()
			require.NoError(t, err)
		})
	}
}
