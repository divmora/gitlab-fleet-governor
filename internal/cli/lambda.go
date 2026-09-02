package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
	"github.com/spf13/cobra"
)

type lambdaFlags struct {
	EventFile string
	Pretty    bool
}

func newLambdaCmd() *cobra.Command {
	var flags lambdaFlags

	cmd := &cobra.Command{
		Use:   "lambda",
		Short: "Emulate local AWS Lambda invocation with a JSON event payload file",
		Long: `Lambda emulates the AWS Lambda execution environment locally by reading a JSON
event payload (EventBridge scheduled event, S3 ObjectCreated event, or direct JSON
invocation), executing the central Lambda handler in-process, and outputting the
structured Lambda response envelope.`,
		Example: `  # Emulate EventBridge scheduled trigger
  gitlab-fleet-governor lambda --event events/scheduled.json

  # Emulate S3 Put Object event trigger
  gitlab-fleet-governor lambda --event events/s3_put.json

  # Emulate event from standard input
  cat events/direct.json | gitlab-fleet-governor lambda --event -`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.EventFile == "" {
				return fmt.Errorf("required flag --event / -e not specified")
			}

			return executeLambda(cmd, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.EventFile, "event", "e", "", "Path to JSON event payload file, or '-' for stdin (required)")
	cmd.Flags().BoolVar(&flags.Pretty, "pretty", true, "Pretty-print output JSON response")

	_ = cmd.MarkFlagRequired("event")

	return cmd
}

func executeLambda(cmd *cobra.Command, flags lambdaFlags) error {
	ctx := cmd.Context()

	// 1. Read event payload
	var rawEvent []byte
	var err error

	if flags.EventFile == "-" {
		rawEvent, err = io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return fmt.Errorf("failed to read event payload from stdin: %w", err)
		}
	} else {
		cleanPath := filepath.Clean(flags.EventFile)
		rawEvent, err = os.ReadFile(cleanPath)
		if err != nil {
			return fmt.Errorf("failed to read event payload file '%s': %w", cleanPath, err)
		}
	}

	if len(rawEvent) == 0 {
		rawEvent = []byte("{}")
	}

	// 2. Instantiate AWS Lambda Handler
	handler := lambda.NewHandler()

	// 3. Dispatch and Handle Request
	resp, err := handler.HandleRequest(ctx, json.RawMessage(rawEvent))
	if err != nil {
		return fmt.Errorf("lambda handler execution error: %w", err)
	}

	// 4. Format and Render Output
	var outBytes []byte
	if flags.Pretty {
		outBytes, err = json.MarshalIndent(resp, "", "  ")
	} else {
		outBytes, err = json.Marshal(resp)
	}
	if err != nil {
		return fmt.Errorf("failed to serialize lambda response: %w", err)
	}

	outWriter, cleanup, outErr := getOutputWriter(cmd, globalFlags.OutputFile)
	if outErr != nil {
		return outErr
	}
	defer cleanup()

	fmt.Fprintln(outWriter, string(outBytes))
	return nil
}
