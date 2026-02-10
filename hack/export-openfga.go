// export-openfga reads schema (authorization models) and relationship tuples
// from an OpenFGA server and prints them as YAML to stdout.
package main

import (
	"context"
	"fmt"
	"os"

	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/go-sdk/client"
	"sigs.k8s.io/yaml"
)

const defaultAPIURL = "http://localhost:8080"

func main() {
	apiURL := os.Getenv("FGA_API_URL")
	if apiURL == "" {
		apiURL = defaultAPIURL
	}
	storeID := os.Getenv("FGA_STORE_ID")
	storeName := os.Getenv("FGA_STORE_NAME")
	if storeID == "" && storeName == "" {
		fmt.Fprintf(os.Stderr, "FGA_STORE_ID or FGA_STORE_NAME must be set\n")
		os.Exit(1)
	}
	if storeID == "" {
		var err error
		storeID, err = resolveStoreIDByName(apiURL, storeName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve store by name: %v\n", err)
			os.Exit(1)
		}
	}

	fgaClient, err := client.NewSdkClient(&client.ClientConfiguration{
		ApiUrl:  apiURL,
		StoreId: storeID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "openfga client: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	models, err := fetchAllAuthorizationModels(ctx, fgaClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read authorization models: %v\n", err)
		os.Exit(1)
	}

	tuples, err := fetchAllTuples(ctx, fgaClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read tuples: %v\n", err)
		os.Exit(1)
	}

	out := map[string]interface{}{
		"authorization_models": models,
		"tuples":               tuples,
	}
	b, err := yaml.Marshal(out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(b); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}

// resolveStoreIDByName lists stores and returns the ID of the first store whose name matches.
func resolveStoreIDByName(apiURL, name string) (string, error) {
	return resolveStoreIDByListStores(apiURL, name)
}

func resolveStoreIDByListStores(apiURL, name string) (string, error) {
	cfg := &client.ClientConfiguration{ApiUrl: apiURL, StoreId: ""}
	c, err := client.NewSdkClient(cfg)
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	var pageToken string
	for {
		req := c.APIClient.OpenFgaApi.ListStores(ctx).PageSize(100)
		if pageToken != "" {
			req = req.ContinuationToken(pageToken)
		}
		resp, _, err := req.Execute()
		if err != nil {
			return "", err
		}
		for i := range resp.Stores {
			if resp.Stores[i].Name == name {
				return resp.Stores[i].Id, nil
			}
		}
		pageToken = resp.ContinuationToken
		if pageToken == "" {
			break
		}
	}
	return "", fmt.Errorf("store named %q not found", name)
}

func fetchAllAuthorizationModels(ctx context.Context, c *client.OpenFgaClient) ([]openfga.AuthorizationModel, error) {
	var all []openfga.AuthorizationModel
	var pageSize int32 = 100
	var token string

	for {
		opts := client.ClientReadAuthorizationModelsOptions{
			PageSize:          openfga.PtrInt32(pageSize),
			ContinuationToken: nil,
		}
		if token != "" {
			opts.ContinuationToken = openfga.PtrString(token)
		}

		resp, err := c.ReadAuthorizationModels(ctx).Options(opts).Execute()
		if err != nil {
			return nil, err
		}

		all = append(all, resp.AuthorizationModels...)
		if resp.ContinuationToken == nil || *resp.ContinuationToken == "" {
			break
		}
		token = *resp.ContinuationToken
	}

	return all, nil
}

func fetchAllTuples(ctx context.Context, c *client.OpenFgaClient) ([]openfga.Tuple, error) {
	var all []openfga.Tuple
	var pageSize int32 = 100
	var token string

	for {
		opts := client.ClientReadOptions{
			PageSize:          openfga.PtrInt32(pageSize),
			ContinuationToken: nil,
		}
		if token != "" {
			opts.ContinuationToken = openfga.PtrString(token)
		}

		// Empty body = read all tuples in the store
		resp, err := c.Read(ctx).Body(client.ClientReadRequest{}).Options(opts).Execute()
		if err != nil {
			return nil, err
		}

		all = append(all, resp.Tuples...)
		if resp.ContinuationToken == "" {
			break
		}
		token = resp.ContinuationToken
	}

	return all, nil
}
