package azure

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-pipeline-go/pipeline"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"github.com/Azure/azure-sdk-for-go/services/storage/mgmt/2019-06-01/storage"
	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/Azure/go-autorest/autorest"
	autorestazure "github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/to"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/rand"
	kcorelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	operatorapiv1 "github.com/openshift/api/operator/v1"

	regopclient "github.com/openshift/cluster-image-registry-operator/pkg/client"
	"github.com/openshift/cluster-image-registry-operator/pkg/defaults"
	"github.com/openshift/cluster-image-registry-operator/pkg/envvar"
	"github.com/openshift/cluster-image-registry-operator/pkg/storage/util"
)

const (
	storageExistsReasonNotConfigured     = "StorageNotConfigured"
	storageExistsReasonConfigError       = "ConfigError"
	storageExistsReasonUserManaged       = "UserManaged"
	storageExistsReasonAzureError        = "AzureError"
	storageExistsReasonContainerNotFound = "ContainerNotFound"
	storageExistsReasonContainerExists   = "ContainerExists"
	storageExistsReasonContainerDeleted  = "ContainerDeleted"
	storageExistsReasonAccountDeleted    = "AccountDeleted"

	defaultPollingDelay    = 10 * time.Second
	defaultPollingDuration = 3 * time.Minute
	defaultRetryAttempts   = 1
)

// storageAccountInvalidCharRe is a regular expression for characters that
// cannot be used in Azure storage accounts names (i.e. that are not
// numbers nor lower-case letters) and that are not upper-case letters. If
// you use this regular expression to filter invalid characters, you also
// need to strings.ToLower to get a valid storage account name or an empty
// string.
var storageAccountInvalidCharRe = regexp.MustCompile(`[^0-9A-Za-z]`)

// Azure holds configuration used to reach Azure's endpoints.
type Azure struct {
	// IPI
	SubscriptionID string
	ClientID       string
	ClientSecret   string
	TenantID       string
	ResourceGroup  string
	Region         string

	// UPI
	AccountKey string
}

type errDoesNotExist struct {
	Err error
}

func (e *errDoesNotExist) Error() string {
	return e.Err.Error()
}

// GetConfig reads configuration for the Azure cloud platform services. It first attempts to
// load credentials from ImageRegistryPrivateConfigurationUser secret, if this secret is not
// present this function loads credentials from cluster wide config present on secret
// CloudCredentialsName.
func GetConfig(secLister kcorelisters.SecretNamespaceLister) (*Azure, error) {
	sec, err := secLister.Get(defaults.ImageRegistryPrivateConfigurationUser)
	if err != nil {
		if !errors.IsNotFound(err) {
			return nil, fmt.Errorf("unable to get user provided secrets: %s", err)
		}

		// loads cluster wide configuration.
		if sec, err = secLister.Get(defaults.CloudCredentialsName); err != nil {
			return nil, fmt.Errorf("unable to get cluster minted credentials: %s", err)
		}

		return &Azure{
			SubscriptionID: string(sec.Data["azure_subscription_id"]),
			ClientID:       string(sec.Data["azure_client_id"]),
			ClientSecret:   string(sec.Data["azure_client_secret"]),
			TenantID:       string(sec.Data["azure_tenant_id"]),
			ResourceGroup:  string(sec.Data["azure_resourcegroup"]),
			Region:         string(sec.Data["azure_region"]),
		}, nil
	}

	// loads user provided account key.
	key, err := util.GetValueFromSecret(sec, "REGISTRY_STORAGE_AZURE_ACCOUNTKEY")
	if err != nil {
		return nil, err
	} else if key == "" {
		return nil, fmt.Errorf("the secret %s/%s has an empty value for "+
			"REGISTRY_STORAGE_AZURE_ACCOUNTKEY; the secret should be removed so that "+
			"the operator can use cluster-wide secrets or it should contain a valid "+
			"storage account access key", sec.Namespace, sec.Name,
		)
	}

	return &Azure{
		AccountKey: key,
	}, nil
}

func getEnvironmentByName(name string) (autorestazure.Environment, error) {
	if name == "" {
		return autorestazure.PublicCloud, nil
	}
	return autorestazure.EnvironmentFromName(name)
}

// generateAccountName returns a name that can be used for an Azure Storage
// Account. Storage account names must be between 3 and 24 characters in
// length and use numbers and lower-case letters only.
func generateAccountName(infrastructureName string) string {
	prefix := "imageregistry" + storageAccountInvalidCharRe.ReplaceAllString(infrastructureName, "")
	if len(prefix) > 24-5 {
		prefix = prefix[:24-5]
	}
	prefix = prefix + rand.String(5)
	return strings.ToLower(prefix)
}

func getBlobServiceURL(environment autorestazure.Environment, accountName string) (*url.URL, error) {
	return url.Parse("https://" + accountName + ".blob." + environment.StorageEndpointSuffix)
}

func (d *driver) accountExists(storageAccountsClient storage.AccountsClient, accountName string) (storage.CheckNameAvailabilityResult, error) {
	return storageAccountsClient.CheckNameAvailability(
		d.Context,
		storage.AccountCheckNameAvailabilityParameters{
			Name: to.StringPtr(accountName),
			Type: to.StringPtr("Microsoft.Storage/storageAccounts"),
		},
	)
}

func (d *driver) createPrivateEndpoint(
	privateEndpointsClient *armnetwork.PrivateEndpointsClient,
	resourceGroupName,
	privateEndpointName,
	accountName,
	location,
	subscriptionID,
	cloudName string,
	tagset map[string]*string,
) (*armnetwork.PrivateEndpoint, error) {
	klog.Infof(
		"attempt to create azure private endpoint %s (resourceGroup=%q, location=%q)...",
		privateEndpointName, resourceGroupName, location,
	)

	vnetName := "fmissi-ms799-vnet"            // TODO: figure out where to get this from
	subnetName := "fmissi-ms799-worker-subnet" // TODO: figure out where to get this from

	// TODO: is there a better way to build this?
	subnetID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s",
		subscriptionID,
		resourceGroupName,
		vnetName,
		subnetName,
	)
	// TODO: is there a better way to build this?
	privateLinkResource := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s",
		subscriptionID,
		resourceGroupName,
		accountName,
	)
	targetSubResource := "blob" // TODO: is there a constant we can use here instead?

	params := armnetwork.PrivateEndpoint{
		Location: to.StringPtr(location),
		Tags:     tagset,
		Properties: &armnetwork.PrivateEndpointProperties{
			CustomNetworkInterfaceName: to.StringPtr(fmt.Sprintf("%s-nic", privateEndpointName)),
			Subnet:                     &armnetwork.Subnet{ID: to.StringPtr(subnetID)},
			PrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{{
				Name: to.StringPtr(privateEndpointName),
				Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
					PrivateLinkServiceID: to.StringPtr(privateLinkResource),
					GroupIDs:             []*string{to.StringPtr(targetSubResource)},
				},
			}},
		},
	}

	pollersResp, err := privateEndpointsClient.BeginCreateOrUpdate(
		d.Context,
		resourceGroupName,
		privateEndpointName,
		params,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start creating private endpoint: %s", err)
	}
	resp, err := pollersResp.PollUntilDone(d.Context, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to finish creating private endpoint: %s", err)
	}

	// ??? set Subnet.PrivateEndpointNetworkPolicies: to.StringPtr("Enabled") ???

	klog.Infof("azure private endpoint %s has been created", privateEndpointName)
	return &resp.PrivateEndpoint, nil
}

func (d *driver) createRecordSet(
	client *armprivatedns.RecordSetsClient,
	nicClient *armnetwork.InterfacesClient,
	privateEndpoint *armnetwork.PrivateEndpoint,
	resourceGroupName,
	accountName,
	privateZoneName string,
) error {
	relativeRecordSetName := accountName
	klog.Infof(
		"attempt to create azure record set %s (resourceGroup=%q)...",
		relativeRecordSetName,
		resourceGroupName,
	)

	if len(privateEndpoint.Properties.NetworkInterfaces) == 0 {
		return fmt.Errorf("private endpoint %s did not have any network interfaces", *privateEndpoint.Name)
	}
	nic := privateEndpoint.Properties.NetworkInterfaces[0]
	nicIDParts := strings.Split(*nic.ID, "/")
	nicName := nicIDParts[len(nicIDParts)-1]
	// klog.Infof(
	// 	"split nic name: %s -- nic name from private endpoint: %s",
	// 	nicName, *nic.Name,
	// )
	resp, err := nicClient.Get(d.Context, resourceGroupName, nicName, nil)
	if err != nil {
		return err
	}
	respNIC := resp.Interface
	if len(respNIC.Properties.IPConfigurations) == 0 {
		return fmt.Errorf("network interface %s did not have any IP configurations", *respNIC.Name)
	}
	// this is auto-created by Azure and there should always ever be one.
	nicAddress := respNIC.Properties.IPConfigurations[0].Properties.PrivateIPAddress

	rs := armprivatedns.RecordSet{
		Properties: &armprivatedns.RecordSetProperties{
			TTL: to.Int64Ptr(10),
			ARecords: []*armprivatedns.ARecord{{
				IPv4Address: nicAddress,
			}},
		},
	}
	_, err = client.CreateOrUpdate(
		d.Context,
		resourceGroupName,
		privateZoneName,
		armprivatedns.RecordTypeA,
		relativeRecordSetName,
		rs,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to create record set: %s", err)
	}
	klog.Infof("azure record set %s has been created", relativeRecordSetName)
	return nil
}

func (d *driver) createPrivateDNSZoneGroup(
	client *armnetwork.PrivateDNSZoneGroupsClient,
	subscriptionID,
	resourceGroupName,
	privateEndpointName,
	privateZoneName string,
) error {
	privateZoneID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/privateDnsZones/%s",
		subscriptionID,
		resourceGroupName,
		privateZoneName,
	)
	groupName := strings.Replace(privateZoneName, ".", "-", -1)
	group := armnetwork.PrivateDNSZoneGroup{
		Name: to.StringPtr(fmt.Sprintf("%s/default", privateZoneName)),
		Properties: &armnetwork.PrivateDNSZoneGroupPropertiesFormat{
			PrivateDNSZoneConfigs: []*armnetwork.PrivateDNSZoneConfig{{
				Name: to.StringPtr("privatelink-blob-core-windows-net"),
				Properties: &armnetwork.PrivateDNSZonePropertiesFormat{
					PrivateDNSZoneID: to.StringPtr(privateZoneID),
				},
			}},
		},
	}
	pollersResp, err := client.BeginCreateOrUpdate(
		d.Context,
		resourceGroupName,
		privateEndpointName,
		groupName,
		group,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to start creating private DNS zone group: %s", err)
	}
	_, err = pollersResp.PollUntilDone(d.Context, nil)
	if err != nil {
		return fmt.Errorf("failed to finish creating private DNS zone group: %s", err)
	}
	return nil
}

func (d *driver) createVirtualNetworkLink(
	client *armprivatedns.VirtualNetworkLinksClient,
	subscriptionID,
	resourceGroupName,
	privateZoneName,
	vnetName string,
	tagset map[string]*string,
) error {
	// * TODO: add virtual network link to private DNS zone (how?)
	virtualNetworkLinkName := "whatever123"
	location := "global"
	vnetID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s",
		subscriptionID,
		resourceGroupName,
		vnetName,
	)
	pollersResp, err := client.BeginCreateOrUpdate(
		d.Context,
		resourceGroupName,
		privateZoneName,
		virtualNetworkLinkName,
		armprivatedns.VirtualNetworkLink{
			Location: to.StringPtr(location),
			Tags:     tagset,
			Properties: &armprivatedns.VirtualNetworkLinkProperties{
				RegistrationEnabled: to.BoolPtr(false),
				VirtualNetwork:      &armprivatedns.SubResource{ID: to.StringPtr(vnetID)},
			},
		},
		nil,
	)
	if err != nil {
		return err
	}
	_, err = pollersResp.PollUntilDone(d.Context, nil)
	if err != nil {
		return err
	}
	return nil
}

func (d *driver) createPrivateDNSZone(
	client *armprivatedns.PrivateZonesClient,
	resourceGroupName,
	cloudName,
	privateZoneName string,
	tagset map[string]*string,
) error {
	// TODO: call this somewhere
	location := "global"
	pollersResp, err := client.BeginCreateOrUpdate(
		d.Context,
		resourceGroupName,
		privateZoneName,
		armprivatedns.PrivateZone{
			Location: to.StringPtr(location),
			Tags:     tagset,
		},
		nil,
	)
	if err != nil {
		return err
	}
	_, err = pollersResp.PollUntilDone(d.Context, nil)
	if err != nil {
		return err
	}
	return nil
}

func (d *driver) createStorageAccount(storageAccountsClient storage.AccountsClient, resourceGroupName, accountName, location, cloudName string, tagset map[string]*string) error {
	klog.Infof("attempt to create azure storage account %s (resourceGroup=%q, location=%q)...", accountName, resourceGroupName, location)

	kind := storage.StorageV2
	params := &storage.AccountPropertiesCreateParameters{
		EnableHTTPSTrafficOnly: to.BoolPtr(true),
		AllowBlobPublicAccess:  to.BoolPtr(false),
		MinimumTLSVersion:      storage.TLS12,
	}

	if strings.EqualFold(cloudName, "AZURESTACKCLOUD") {
		// It seems Azure Stack Hub does not support new API.
		kind = storage.Storage
		params = &storage.AccountPropertiesCreateParameters{}
	}

	future, err := storageAccountsClient.Create(
		d.Context,
		resourceGroupName,
		accountName,
		storage.AccountCreateParameters{
			Kind:     kind,
			Location: to.StringPtr(location),
			Sku: &storage.Sku{
				Name: storage.StandardLRS,
			},
			AccountPropertiesCreateParameters: params,
			Tags:                              tagset,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to start creating storage account: %s", err)
	}

	// TODO: this may take up to 10 minutes
	err = future.WaitForCompletionRef(d.Context, storageAccountsClient.Client)
	if err != nil {
		return fmt.Errorf("failed to finish creating storage account: %s", err)
	}

	_, err = future.Result(storageAccountsClient)
	if err != nil {
		return fmt.Errorf("failed to create storage account: %s", err)
	}

	klog.Infof("azure storage account %s has been created", accountName)

	return nil
}

func (d *driver) getAccountPrimaryKey(storageAccountsClient storage.AccountsClient, resourceGroupName, accountName string) (string, error) {
	key, err := primaryKey.get(d.Context, storageAccountsClient, resourceGroupName, accountName)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to get keys for the storage account %s: %s", accountName, err)
		if e, ok := err.(autorest.DetailedError); ok {
			if e.StatusCode == http.StatusNotFound {
				return "", &errDoesNotExist{Err: wrappedErr}
			}
		}
		return "", wrappedErr
	}

	return key, nil
}

func (d *driver) getStorageContainer(environment autorestazure.Environment, accountName, key, containerName string) (azblob.ContainerURL, error) {
	c, err := azblob.NewSharedKeyCredential(accountName, key)
	if err != nil {
		return azblob.ContainerURL{}, err
	}

	p := azblob.NewPipeline(c, azblob.PipelineOptions{
		Telemetry:  azblob.TelemetryOptions{Value: defaults.UserAgent},
		HTTPSender: d.httpSender,
	})

	u, err := getBlobServiceURL(environment, accountName)
	if err != nil {
		return azblob.ContainerURL{}, err
	}

	service := azblob.NewServiceURL(*u, p)
	return service.NewContainerURL(containerName), nil
}

func (d *driver) createStorageContainer(environment autorestazure.Environment, accountName, key, containerName string) error {
	container, err := d.getStorageContainer(environment, accountName, key, containerName)
	if err != nil {
		return err
	}

	_, err = container.Create(d.Context, azblob.Metadata{}, azblob.PublicAccessNone)
	return err
}

func (d *driver) deleteStorageContainer(environment autorestazure.Environment, accountName, key, containerName string) error {
	container, err := d.getStorageContainer(environment, accountName, key, containerName)
	if err != nil {
		return err
	}

	_, err = container.Delete(d.Context, azblob.ContainerAccessConditions{})
	return err
}

type driver struct {
	// Context holds the operator's context that was passed to NewDriver.
	Context context.Context

	// Config is a subset of the image registry config. It may contain config
	// from spec or status depending on the caller intention.
	Config *imageregistryv1.ImageRegistryConfigStorageAzure

	// Listers is a collection of listers that the driver can use to obtain
	// additional objects from the cluster.
	Listers *regopclient.StorageListers

	// authorizer is for Azure autorest generated clients.
	// Added as a member to the struct to allow injection for testing.
	authorizer autorest.Authorizer

	// sender is for Azure autorest generated clients.
	// Added as a member to the struct to allow injection for testing.
	sender autorest.Sender

	// httpSender is for Azure Pipeline.
	// Added as a member to the struct to allow injection for testing.
	httpSender pipeline.Factory
}

// NewDriver creates a new storage driver for Azure Blob Storage.
func NewDriver(ctx context.Context, c *imageregistryv1.ImageRegistryConfigStorageAzure, listers *regopclient.StorageListers) *driver {
	return &driver{
		Context: ctx,
		Config:  c,
		Listers: listers,
	}
}

func (d *driver) storageAccountsClient(cfg *Azure, environment autorestazure.Environment) (storage.AccountsClient, error) {
	storageAccountsClient := storage.NewAccountsClientWithBaseURI(environment.ResourceManagerEndpoint, cfg.SubscriptionID)
	storageAccountsClient.PollingDelay = defaultPollingDelay
	storageAccountsClient.PollingDuration = defaultPollingDuration
	storageAccountsClient.RetryAttempts = defaultRetryAttempts
	_ = storageAccountsClient.AddToUserAgent(defaults.UserAgent)

	storageAccountsClient.Authorizer = d.authorizer
	if d.authorizer == nil {
		authz, err := authorizer(cfg, environment)
		if err != nil {
			return storage.AccountsClient{}, err
		}
		storageAccountsClient.Authorizer = authz
	}

	if d.sender != nil {
		storageAccountsClient.Sender = d.sender
	}

	return storageAccountsClient, nil
}

func (d *driver) privateEndpointsClient(cfg *Azure, environment autorestazure.Environment) (*armnetwork.PrivateEndpointsClient, error) {
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
	cliopts := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Retry: policy.RetryOptions{
				MaxRetries: -1, // try once
			},
		},
	}
	if d.sender != nil {
		cliopts.ClientOptions.Transport = d.sender
	}
	client, err := armnetwork.NewPrivateEndpointsClient(cfg.SubscriptionID, cred, cliopts)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (d *driver) privateZonesClient(cfg *Azure, environment autorestazure.Environment) (*armprivatedns.PrivateZonesClient, error) {
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
	cliopts := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Retry: policy.RetryOptions{
				MaxRetries: -1, // try once
			},
		},
	}
	if d.sender != nil {
		cliopts.ClientOptions.Transport = d.sender
	}
	client, err := armprivatedns.NewPrivateZonesClient(cfg.SubscriptionID, cred, cliopts)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (d *driver) recordSetsClient(cfg *Azure, environment autorestazure.Environment) (*armprivatedns.RecordSetsClient, error) {
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
	cliopts := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Retry: policy.RetryOptions{
				MaxRetries: -1, // try once
			},
		},
	}
	if d.sender != nil {
		cliopts.ClientOptions.Transport = d.sender
	}
	client, err := armprivatedns.NewRecordSetsClient(cfg.SubscriptionID, cred, cliopts)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (d *driver) privateZoneGroupsClient(cfg *Azure, environment autorestazure.Environment) (*armnetwork.PrivateDNSZoneGroupsClient, error) {
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
	cliopts := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Retry: policy.RetryOptions{
				MaxRetries: -1, // try once
			},
		},
	}
	if d.sender != nil {
		cliopts.ClientOptions.Transport = d.sender
	}
	client, err := armnetwork.NewPrivateDNSZoneGroupsClient(cfg.SubscriptionID, cred, cliopts)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (d *driver) vnetLinksClient(cfg *Azure, environment autorestazure.Environment) (*armprivatedns.VirtualNetworkLinksClient, error) {
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
	cliopts := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Retry: policy.RetryOptions{
				MaxRetries: -1, // try once
			},
		},
	}
	if d.sender != nil {
		cliopts.ClientOptions.Transport = d.sender
	}
	client, err := armprivatedns.NewVirtualNetworkLinksClient(cfg.SubscriptionID, cred, cliopts)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (d *driver) interfacesClient(cfg *Azure, environment autorestazure.Environment) (*armnetwork.InterfacesClient, error) {
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
	cliopts := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Retry: policy.RetryOptions{
				MaxRetries: -1, // try once
			},
		},
	}
	if d.sender != nil {
		cliopts.ClientOptions.Transport = d.sender
	}
	client, err := armnetwork.NewInterfacesClient(cfg.SubscriptionID, cred, cliopts)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (d *driver) getKey(cfg *Azure, environment autorestazure.Environment) (string, error) {
	if cfg.AccountKey != "" {
		return cfg.AccountKey, nil
	}

	storageAccountsClient, err := d.storageAccountsClient(cfg, environment)
	if err != nil {
		return "", err
	}

	key, err := d.getAccountPrimaryKey(storageAccountsClient, cfg.ResourceGroup, d.Config.AccountName)
	if err != nil {
		return "", err
	}

	return key, nil
}

func (d *driver) CABundle() (string, bool, error) {
	return "", true, nil
}

// ConfigEnv configures the environment variables that will be used in the
// image registry deployment.
func (d *driver) ConfigEnv() (envs envvar.List, err error) {
	cfg, err := GetConfig(d.Listers.Secrets)
	if err != nil {
		return nil, err
	}

	environment, err := getEnvironmentByName(d.Config.CloudName)
	if err != nil {
		return nil, err
	}

	key := cfg.AccountKey
	if key == "" {
		storageAccountsClient, err := d.storageAccountsClient(cfg, environment)
		if err != nil {
			return nil, err
		}

		key, err = d.getAccountPrimaryKey(storageAccountsClient, cfg.ResourceGroup, d.Config.AccountName)
		if err != nil {
			return nil, err
		}
	}

	envs = append(envs,
		envvar.EnvVar{Name: "REGISTRY_STORAGE", Value: "azure"},
		envvar.EnvVar{Name: "REGISTRY_STORAGE_AZURE_CONTAINER", Value: d.Config.Container},
		envvar.EnvVar{Name: "REGISTRY_STORAGE_AZURE_ACCOUNTNAME", Value: d.Config.AccountName},
		envvar.EnvVar{Name: "REGISTRY_STORAGE_AZURE_ACCOUNTKEY", Value: key, Secret: true},
	)

	if d.Config.CloudName != "" {
		envs = append(envs, envvar.EnvVar{Name: "REGISTRY_STORAGE_AZURE_REALM", Value: environment.StorageEndpointSuffix})
	}

	return
}

func (d *driver) Volumes() ([]corev1.Volume, []corev1.VolumeMount, error) {
	return nil, nil, nil
}

func (d *driver) VolumeSecrets() (map[string]string, error) {
	return nil, nil
}

// containerExists determines whether or not an azure container exists
func (d *driver) containerExists(ctx context.Context, environment autorestazure.Environment, accountName, key, containerName string) (bool, error) {
	if accountName == "" || containerName == "" {
		return false, nil
	}

	c, err := azblob.NewSharedKeyCredential(accountName, key)
	if err != nil {
		return false, err
	}

	u, err := getBlobServiceURL(environment, accountName)
	if err != nil {
		return false, err
	}

	p := azblob.NewPipeline(c, azblob.PipelineOptions{
		Telemetry:  azblob.TelemetryOptions{Value: defaults.UserAgent},
		HTTPSender: d.httpSender,
	})

	service := azblob.NewServiceURL(*u, p)
	container := service.NewContainerURL(containerName)
	_, err = container.GetProperties(ctx, azblob.LeaseAccessConditions{})
	if e, ok := err.(azblob.StorageError); ok {
		if e.ServiceCode() == azblob.ServiceCodeContainerNotFound {
			return false, nil
		}
	}
	if err != nil {
		return false, fmt.Errorf("unable to get the storage container %s: %s", containerName, err)
	}

	return true, nil
}

// StorageExists checks if the storage container exists and is accessible.
func (d *driver) StorageExists(cr *imageregistryv1.Config) (bool, error) {
	if d.Config.AccountName == "" || d.Config.Container == "" {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionFalse, storageExistsReasonNotConfigured, "Storage is not configured")
		return false, nil
	}

	cfg, err := GetConfig(d.Listers.Secrets)
	if err != nil {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionUnknown, storageExistsReasonConfigError, fmt.Sprintf("Unable to get configuration: %s", err))
		return false, err
	}

	environment, err := getEnvironmentByName(d.Config.CloudName)
	if err != nil {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionUnknown, storageExistsReasonConfigError, fmt.Sprintf("Unable to get cloud environment: %s", err))
		return false, err
	}

	key, err := d.getKey(cfg, environment)
	if err != nil {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionUnknown, storageExistsReasonAzureError, fmt.Sprintf("Unable to get storage account key: %s", err))
		return false, err
	}

	exists, err := d.containerExists(d.Context, environment, d.Config.AccountName, key, d.Config.Container)
	if err != nil {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionUnknown, storageExistsReasonAzureError, fmt.Sprintf("%s", err))
		return false, err
	}
	if !exists {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionFalse, storageExistsReasonContainerNotFound, fmt.Sprintf("Could not find storage container %s", d.Config.Container))
		return false, nil
	}

	util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionTrue, storageExistsReasonContainerExists, "Storage container exists")
	return true, nil
}

// StorageChanged checks if the storage configuration has changed.
func (d *driver) StorageChanged(cr *imageregistryv1.Config) bool {
	return !reflect.DeepEqual(cr.Status.Storage.Azure, cr.Spec.Storage.Azure)
}

// assureStorageAccount makes sure there is a storage account in place and apply any provided tags.
// If no storage account name is provided it attempts to generate one. Returns the account name
// (either the one provided or the one generated), if the account was created or was already there and an error.
func (d *driver) assureStorageAccount(cfg *Azure, infra *configv1.Infrastructure) (string, bool, error) {
	environment, err := getEnvironmentByName(d.Config.CloudName)
	if err != nil {
		return "", false, err
	}

	storageAccountsClient, err := d.storageAccountsClient(cfg, environment)
	if err != nil {
		return "", false, err
	}

	var accountNameGenerated bool
	accountName := d.Config.AccountName
	if accountName == "" {
		accountNameGenerated = true
		accountName = generateAccountName(infra.Status.InfrastructureName)
	}

	result, err := d.accountExists(storageAccountsClient, accountName)
	if err != nil {
		return "", false, err
	}

	// if the generated storage account is not available we return an error.
	if accountNameGenerated && !*result.NameAvailable {
		return "", false, fmt.Errorf("create storage account failed, name not available")
	}

	// Tag the storage account with the openshiftClusterID
	// along with any user defined tags from the cluster configuration
	klog.V(2).Info("setting azure storage account tags")

	tagset := map[string]*string{
		fmt.Sprintf("kubernetes.io_cluster.%s", infra.Status.InfrastructureName): to.StringPtr("owned"),
	}

	// at this stage we are not keeping user tags in sync. as per enhancement proposal
	// we only set user provided tags when we created the bucket.
	hasAzureStatus := infra.Status.PlatformStatus != nil && infra.Status.PlatformStatus.Azure != nil && infra.Status.PlatformStatus.Azure.ResourceTags != nil
	if hasAzureStatus {
		klog.V(5).Infof("user has provided %d tags", len(infra.Status.PlatformStatus.Azure.ResourceTags))
		for _, tag := range infra.Status.PlatformStatus.Azure.ResourceTags {
			klog.V(5).Infof("user has provided storage account tag: %s: %s", tag.Key, tag.Value)
			tagset[tag.Key] = to.StringPtr(tag.Value)
		}
	}
	klog.V(5).Infof("tagging storage account with tags: %+v", tagset)

	// regardless if the storage account name was provided by the user or we generated it,
	// if it is available, we do attempt to create it.
	var storageAccountCreated bool
	if *result.NameAvailable {
		storageAccountCreated = true
		if err := d.createStorageAccount(
			storageAccountsClient, cfg.ResourceGroup, accountName, cfg.Region, d.Config.CloudName, tagset,
		); err != nil {
			return "", false, err
		}

		privateEndpointsClient, err := d.privateEndpointsClient(cfg, environment)
		if err != nil {
			return "", false, err
		}
		privateZonesClient, err := d.privateZonesClient(cfg, environment)
		if err != nil {
			return "", false, err
		}
		recordSetsClient, err := d.recordSetsClient(cfg, environment)
		if err != nil {
			return "", false, err
		}
		privateZoneGroupsClient, err := d.privateZoneGroupsClient(cfg, environment)
		if err != nil {
			return "", false, err
		}
		vnetLinksClient, err := d.vnetLinksClient(cfg, environment)
		if err != nil {
			return "", false, err
		}
		interfacesClient, err := d.interfacesClient(cfg, environment)
		if err != nil {
			return "", false, err
		}

		// TODO: save the private endpoint name in the operator config
		privateEndpointName := generateAccountName(infra.Status.InfrastructureName)
		privateEndpoint, err := d.createPrivateEndpoint(
			privateEndpointsClient,
			cfg.ResourceGroup,
			privateEndpointName,
			accountName,
			cfg.Region,
			cfg.SubscriptionID,
			d.Config.CloudName,
			tagset,
		)
		if err != nil {
			return "", false, err
		}

		privateZoneName := "privatelink.blob.core.windows.net"
		if err := d.createPrivateDNSZone(
			privateZonesClient,
			cfg.ResourceGroup,
			d.Config.CloudName,
			privateZoneName,
			tagset,
		); err != nil {
			return "", false, err
		}
		if err := d.createRecordSet(
			recordSetsClient,
			interfacesClient,
			privateEndpoint,
			cfg.ResourceGroup,
			accountName,
			privateZoneName,
		); err != nil {
			return "", false, err
		}
		if err := d.createPrivateDNSZoneGroup(
			privateZoneGroupsClient,
			cfg.SubscriptionID,
			cfg.ResourceGroup,
			*privateEndpoint.Name,
			privateZoneName,
		); err != nil {
			return "", false, err
		}
		vnetName := "fmissi-ms799-vnet"
		if err := d.createVirtualNetworkLink(
			vnetLinksClient,
			cfg.SubscriptionID,
			cfg.ResourceGroup,
			privateZoneName,
			vnetName,
			tagset,
		); err != nil {
			return "", false, err
		}
	}

	return accountName, storageAccountCreated, nil
}

// assureContainer makes sure we have a container in place. Container name may be provided or
// generated automatically. Returns the container name (the provided one or the automatically
// generated), if the container was created or was already there and an error.
func (d *driver) assureContainer(cfg *Azure) (string, bool, error) {
	environment, err := getEnvironmentByName(d.Config.CloudName)
	if err != nil {
		return "", false, err
	}

	storageAccountsClient, err := d.storageAccountsClient(cfg, environment)
	if err != nil {
		return "", false, err
	}

	key, err := d.getAccountPrimaryKey(
		storageAccountsClient, cfg.ResourceGroup, d.Config.AccountName,
	)
	if err != nil {
		return "", false, err
	}

	if d.Config.Container == "" {
		containerName, err := util.GenerateStorageName(d.Listers, "")
		if err != nil {
			return "", false, err
		}

		if err = d.createStorageContainer(
			environment, d.Config.AccountName, key, containerName,
		); err != nil {
			return "", false, err
		}

		return containerName, true, nil
	}

	if exists, err := d.containerExists(
		d.Context, environment, d.Config.AccountName, key, d.Config.Container,
	); err != nil {
		return "", false, err
	} else if exists {
		return d.Config.Container, false, nil
	}

	if err = d.createStorageContainer(
		environment, d.Config.AccountName, key, d.Config.Container,
	); err != nil {
		return "", false, err
	}
	return d.Config.Container, true, nil
}

// processUPI verifies if user provided configuration is complete and updates conditions
// and status appropriately.
func (d *driver) processUPI(cr *imageregistryv1.Config) {
	if d.Config.AccountName == "" {
		util.UpdateCondition(
			cr,
			defaults.StorageExists,
			operatorapiv1.ConditionFalse,
			storageExistsReasonNotConfigured,
			"Storage account key is provided, but account name is not specified",
		)
		return
	}

	if d.Config.Container == "" {
		util.UpdateCondition(
			cr,
			defaults.StorageExists,
			operatorapiv1.ConditionFalse,
			storageExistsReasonNotConfigured,
			"Storage account is provided, but container is not specified",
		)
		return
	}

	// We only set the storage management if it is not already set.
	if cr.Spec.Storage.ManagementState == "" {
		cr.Spec.Storage.ManagementState = imageregistryv1.StorageManagementStateUnmanaged
	}

	cr.Status.Storage = imageregistryv1.ImageRegistryConfigStorage{
		Azure: d.Config.DeepCopy(),
	}

	util.UpdateCondition(
		cr,
		defaults.StorageExists,
		operatorapiv1.ConditionTrue,
		storageExistsReasonUserManaged,
		"Storage is managed by the user",
	)
}

// CreateStorage attempts to create a storage account and a storage container.
func (d *driver) CreateStorage(cr *imageregistryv1.Config) error {
	cfg, err := GetConfig(d.Listers.Secrets)
	if err != nil {
		util.UpdateCondition(
			cr,
			defaults.StorageExists,
			operatorapiv1.ConditionUnknown,
			storageExistsReasonConfigError,
			fmt.Sprintf("Unable to get configuration: %s", err),
		)
		return err
	}

	// if AccountKey is present in our configuration it means it was provided by the user
	// so we only verify if everything we need is in place.
	if cfg.AccountKey != "" {
		d.processUPI(cr)
		return nil
	}

	infra, err := util.GetInfrastructure(d.Listers)
	if err != nil {
		util.UpdateCondition(
			cr,
			defaults.StorageExists,
			operatorapiv1.ConditionUnknown,
			storageExistsReasonConfigError,
			fmt.Sprintf("Unable to get infrastructure: %s", err),
		)
		return err
	}

	if d.Config.CloudName == "" && d.Config.AccountName == "" {
		platformStatus := infra.Status.PlatformStatus
		if platformStatus != nil &&
			platformStatus.Type == configv1.AzurePlatformType &&
			platformStatus.Azure != nil {
			d.Config.CloudName = string(platformStatus.Azure.CloudName)
		}
	}

	storageAccountName, storageAccountCreated, err := d.assureStorageAccount(cfg, infra)
	if err != nil {
		util.UpdateCondition(
			cr,
			defaults.StorageExists,
			operatorapiv1.ConditionUnknown,
			storageExistsReasonAzureError,
			fmt.Sprintf("Unable to process storage account: %s", err),
		)
		return err
	}
	d.Config.AccountName = storageAccountName

	containerName, containerCreated, err := d.assureContainer(cfg)
	if err != nil {
		util.UpdateCondition(
			cr,
			defaults.StorageExists,
			operatorapiv1.ConditionUnknown,
			storageExistsReasonAzureError,
			fmt.Sprintf("Unable to process storage container: %s", err),
		)
		return err
	}
	d.Config.Container = containerName

	// We only set the storage management if it is not already set.
	if cr.Spec.Storage.ManagementState == "" {
		if storageAccountCreated || containerCreated {
			cr.Spec.Storage.ManagementState = imageregistryv1.StorageManagementStateManaged
		} else {
			cr.Spec.Storage.ManagementState = imageregistryv1.StorageManagementStateUnmanaged
		}
	}

	cr.Spec.Storage.Azure = d.Config.DeepCopy()
	cr.Status.Storage = imageregistryv1.ImageRegistryConfigStorage{
		Azure: d.Config.DeepCopy(),
	}

	util.UpdateCondition(
		cr,
		defaults.StorageExists,
		operatorapiv1.ConditionTrue,
		storageExistsReasonContainerExists,
		"Storage container exists",
	)
	return nil
}

// RemoveStorage deletes the storage medium that was created.
func (d *driver) RemoveStorage(cr *imageregistryv1.Config) (retry bool, err error) {
	if cr.Spec.Storage.ManagementState != imageregistryv1.StorageManagementStateManaged {
		return false, nil
	}
	if d.Config.AccountName == "" {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionFalse, storageExistsReasonNotConfigured, "Storage is not configured")
		return false, nil
	}

	cfg, err := GetConfig(d.Listers.Secrets)
	if err != nil {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionUnknown, storageExistsReasonConfigError, fmt.Sprintf("Unable to get configuration: %s", err))
		return false, err
	}

	environment, err := getEnvironmentByName(d.Config.CloudName)
	if err != nil {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionUnknown, storageExistsReasonConfigError, fmt.Sprintf("Unable to get cloud environment: %s", err))
		return false, err
	}

	storageAccountsClient, err := d.storageAccountsClient(cfg, environment)
	if err != nil {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionUnknown, storageExistsReasonAzureError, fmt.Sprintf("Unable to get accounts client: %s", err))
		return false, err
	}

	if d.Config.Container != "" {
		key, err := d.getAccountPrimaryKey(storageAccountsClient, cfg.ResourceGroup, d.Config.AccountName)
		if _, ok := err.(*errDoesNotExist); ok {
			d.Config.AccountName = ""
			cr.Spec.Storage.Azure.AccountName = "" // TODO
			cr.Status.Storage.Azure.AccountName = ""
			util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionFalse, storageExistsReasonContainerNotFound, fmt.Sprintf("Container has been already deleted: %s", err))
			return false, nil
		}
		if err != nil {
			util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionUnknown, storageExistsReasonAzureError, fmt.Sprintf("Unable to get account primary keys: %s", err))
			return false, err
		}

		err = d.deleteStorageContainer(environment, d.Config.AccountName, key, d.Config.Container)
		if err != nil {
			util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionUnknown, storageExistsReasonAzureError, fmt.Sprintf("Unable to delete storage container: %s", err))
			return false, err // TODO: is it retryable?
		}

		d.Config.Container = ""
		cr.Spec.Storage.Azure.Container = "" // TODO: what if it was provided by a user?
		cr.Status.Storage.Azure.Container = ""
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionFalse, storageExistsReasonContainerDeleted, "Storage container has been deleted")
	}

	_, err = storageAccountsClient.Delete(d.Context, cfg.ResourceGroup, d.Config.AccountName)
	if err != nil {
		util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionFalse, storageExistsReasonAzureError, fmt.Sprintf("Unable to delete storage account: %s", err))
		return false, err
	}

	d.Config.AccountName = ""
	cr.Spec.Storage.Azure.AccountName = "" // TODO
	cr.Status.Storage.Azure.AccountName = ""
	util.UpdateCondition(cr, defaults.StorageExists, operatorapiv1.ConditionFalse, storageExistsReasonAccountDeleted, "Storage account has been deleted")

	return false, nil
}

// ID return the underlying storage identificator, on this case the Azure
// container name.
func (d *driver) ID() string {
	return d.Config.Container
}
