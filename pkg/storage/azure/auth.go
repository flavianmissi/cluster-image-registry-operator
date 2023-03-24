package azure

import (
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/go-autorest/autorest"
	autorestazure "github.com/Azure/go-autorest/autorest/azure"
	"github.com/jongio/azidext/go/azidext"
)

func authorizer(cfg *Azure, environment autorestazure.Environment) (autorest.Authorizer, error) {
	cloudConfig := cloud.Configuration{
		ActiveDirectoryAuthorityHost: environment.ActiveDirectoryEndpoint,
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Audience: environment.TokenAudience,
				Endpoint: environment.ResourceManagerEndpoint,
			},
		},
	}
	options := azidentity.ClientSecretCredentialOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloudConfig,
		},
	}
	cred, err := azidentity.NewClientSecretCredential(cfg.TenantID, cfg.ClientID, cfg.ClientSecret, &options)
	if err != nil {
		return nil, err
	}
	scope := environment.TokenAudience
	if !strings.HasSuffix(scope, "/.default") {
		scope += "/.default"
	}

	return azidext.NewTokenCredentialAdapter(cred, []string{scope}), nil
}
