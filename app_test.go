package flamingo_test

import (
	"fmt"
	"testing"

	"flamingo.me/dingo"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"flamingo.me/flamingo/v3"
	"flamingo.me/flamingo/v3/framework/cmd"
)

func TestAppErrorHandler(t *testing.T) {
	tests := []struct {
		name         string
		args         string
		runE         func(cmd *cobra.Command, args []string) error
		run          func(cmd *cobra.Command, args []string)
		errorHandler func(err error)
	}{
		{
			name: "execute RunE command",
			args: "test_cmd_run_e",
			runE: func(cc *cobra.Command, _ []string) error {
				assert.Equal(t, "test_cmd_run_e", cc.Short)
				return fmt.Errorf("%w: test_cmd_run_e", cmd.ErrCmdRun)
			},
			errorHandler: func(err error) {
				assert.ErrorIs(t, err, cmd.ErrCmdRun)
				assert.ErrorContains(t, err, "test_cmd_run_e")
			},
		},
		{
			name: "execute Run command",
			args: "test_cmd_run",
			run: func(cc *cobra.Command, _ []string) {
				assert.Equal(t, "test_cmd_run", cc.Short)
			},
			errorHandler: func(err error) {
				assert.NoError(t, err)
			},
		},
	}

	for _, tt := range tests { //nolint:paralleltest // because of dingo.Singleton
		t.Run(tt.name, func(t *testing.T) {
			modules := []dingo.Module{
				dingo.ModuleFunc(func(injector *dingo.Injector) {
					injector.BindMulti(new(cobra.Command)).ToInstance(&cobra.Command{
						Use:   "test_cmd_run_e",
						Short: "test_cmd_run_e",
						RunE:  tt.runE,
					})

					injector.BindMulti(new(cobra.Command)).ToInstance(&cobra.Command{
						Use:   "test_cmd_run",
						Short: "test_cmd_run",
						Run:   tt.run,
					})
				})}

			dingo.Singleton = dingo.NewSingletonScope()
			dingo.ChildSingleton = dingo.NewChildSingletonScope()

			app, err := flamingo.NewApplication(modules,
				flamingo.WithArgs(tt.args),
				flamingo.WithErrorHandler(tt.errorHandler))
			require.NoError(t, err)

			_ = app.Run()
		})
	}

}
