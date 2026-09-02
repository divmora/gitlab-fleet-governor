package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	awslambda "github.com/aws/aws-lambda-go/lambda"
	"github.com/divmora/gitlab-fleet-governor/internal/cli"
	lambdapk "github.com/divmora/gitlab-fleet-governor/internal/lambda"
)

func main() {
	// 1. AWS Lambda Runtime Auto-Detection
	// When running inside AWS Lambda, AWS_LAMBDA_FUNCTION_NAME or AWS_LAMBDA_RUNTIME_API is set.
	if isLambdaEnvironment() {
		handler := lambdapk.NewHandler()
		awslambda.Start(handler.HandleRequest)
		return
	}

	// 2. Standard CLI / Docker Runtime Execution
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := cli.Execute(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// isLambdaEnvironment checks if the current process is running in an AWS Lambda execution context.
func isLambdaEnvironment() bool {
	return os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" ||
		os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" ||
		os.Getenv("_LAMBDA_SERVER_PORT") != ""
}
