package azureclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	autorestazure "github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/to"
)

const (
	targetSubResource          = "blob"
	defaultPrivateZoneName     = "privatelink.blob.core.windows.net"
	defaultPrivateZoneLocation = "global"
	defaultRecordSetTTL        = 10
)

type Client struct {
	creds             azcore.TokenCredential
	clientOpts        *arm.ClientOptions
	tagset            map[string]*string
	subscriptionID    string
	resourceGroupName string
}

type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Options struct {
	Environment        autorestazure.Environment
	TenantID           string
	ClientID           string
	ClientSecret       string
	FederatedTokenFile string
	SubscriptionID     string
	ResourceGroupName  string
	TagSet             map[string]*string
	HTTPClient         Doer
	Creds              azcore.TokenCredential
}

type PrivateEndpointCreateOptions struct {
	Location            string
	VNetName            string
	SubnetName          string
	PrivateEndpointName string
	// The name of an existing storage account
	StorageAccountName string
}

func New(opts *Options) (*Client, error) {
	if err := validate(opts); err != nil {
		return nil, err
	}

	cloudConfig := cloud.Configuration{
		ActiveDirectoryAuthorityHost: opts.Environment.ActiveDirectoryEndpoint,
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Audience: opts.Environment.TokenAudience,
				Endpoint: opts.Environment.ResourceManagerEndpoint,
			},
		},
	}
	coreOpts := azcore.ClientOptions{
		Cloud: cloudConfig,
	}
	if opts.HTTPClient != nil {
		coreOpts.Transport = opts.HTTPClient
	}
	creds := opts.Creds
	if creds == nil {
		var err error
		if strings.TrimSpace(opts.ClientSecret) == "" {
			options := azidentity.WorkloadIdentityCredentialOptions{
				ClientOptions: coreOpts,
				ClientID:      opts.ClientID,
				TenantID:      opts.TenantID,
				TokenFilePath: opts.FederatedTokenFile,
			}
			creds, err = azidentity.NewWorkloadIdentityCredential(&options)
			if err != nil {
				return nil, err
			}
		} else {
			options := azidentity.ClientSecretCredentialOptions{
				ClientOptions: coreOpts,
			}
			creds, err = azidentity.NewClientSecretCredential(
				opts.TenantID,
				opts.ClientID,
				opts.ClientSecret,
				&options,
			)
			if err != nil {
				return nil, err
			}
		}

	}

	coreOpts.Retry = policy.RetryOptions{
		MaxRetries: -1, // try once
	}
	clientOpts := &arm.ClientOptions{
		ClientOptions: coreOpts,
	}

	return &Client{
		creds:             creds,
		clientOpts:        clientOpts,
		tagset:            opts.TagSet,
		subscriptionID:    opts.SubscriptionID,
		resourceGroupName: opts.ResourceGroupName,
	}, nil
}

func (c *Client) getStorageAccount(ctx context.Context, accountName string) (armstorage.Account, error) {
	client, err := armstorage.NewAccountsClient(c.subscriptionID, c.creds, c.clientOpts)
	if err != nil {
		return armstorage.Account{}, fmt.Errorf("failed to create accounts client: %q", err)
	}
	resp, err := client.GetProperties(ctx, c.resourceGroupName, accountName, nil)
	if err != nil {
		return armstorage.Account{}, err
	}
	return resp.Account, nil
}

func (c *Client) UpdateStorageAccountNetworkAccess(ctx context.Context, accountName string, allowPublicAccess bool) error {
	client, err := armstorage.NewAccountsClient(c.subscriptionID, c.creds, c.clientOpts)
	if err != nil {
		return fmt.Errorf("failed to create accounts client: %q", err)
	}
	publicNetworkAccess := armstorage.PublicNetworkAccessDisabled
	if allowPublicAccess {
		publicNetworkAccess = armstorage.PublicNetworkAccessEnabled
	}
	params := armstorage.AccountUpdateParameters{
		Properties: &armstorage.AccountPropertiesUpdateParameters{
			PublicNetworkAccess: &publicNetworkAccess,
		},
	}
	if _, err := client.Update(ctx, c.resourceGroupName, accountName, params, nil); err != nil {
		return err
	}
	return nil
}

// IsStorageAccountPrivate gets a storage account and returns true if public
// network access is disabled, or false if public network access is enabled.
// Public network access is enabled by default in Azure. In case of any
// unexpected behaviour this function will return false.
func (c *Client) IsStorageAccountPrivate(ctx context.Context, accountName string) bool {
	account, err := c.getStorageAccount(ctx, accountName)
	if err != nil {
		return false
	}
	if account.Properties == nil {
		return false
	}
	publicNetworkAccess := account.Properties.PublicNetworkAccess
	if publicNetworkAccess == nil {
		return false
	}
	return *publicNetworkAccess == armstorage.PublicNetworkAccessDisabled
}

func (c *Client) PrivateEndpointExists(ctx context.Context, privateEndpointName string) (bool, error) {
	client, err := armnetwork.NewPrivateEndpointsClient(
		c.subscriptionID,
		c.creds,
		c.clientOpts,
	)
	if err != nil {
		return false, err
	}
	if _, err := client.Get(ctx, c.resourceGroupName, privateEndpointName, nil); err != nil {
		respErr, ok := err.(*azcore.ResponseError)
		if ok && respErr.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *Client) CreatePrivateEndpoint(
	ctx context.Context,
	opts *PrivateEndpointCreateOptions,
) (*armnetwork.PrivateEndpoint, error) {
	client, err := armnetwork.NewPrivateEndpointsClient(
		c.subscriptionID,
		c.creds,
		c.clientOpts,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get private endpoints client: %q", err)
	}

	privateLinkResourceID := formatPrivateLinkResourceID(
		c.subscriptionID,
		c.resourceGroupName,
		opts.StorageAccountName,
	)
	subnetID := formatSubnetID(
		opts.SubnetName,
		opts.VNetName,
		c.resourceGroupName,
		c.subscriptionID,
	)

	privateEndpointName := opts.PrivateEndpointName

	params := armnetwork.PrivateEndpoint{
		Location: to.StringPtr(opts.Location),
		Tags:     c.tagset,
		Properties: &armnetwork.PrivateEndpointProperties{
			CustomNetworkInterfaceName: to.StringPtr(fmt.Sprintf("%s-nic", privateEndpointName)),
			Subnet:                     &armnetwork.Subnet{ID: to.StringPtr(subnetID)},
			PrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{{
				Name: to.StringPtr(privateEndpointName),
				Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
					PrivateLinkServiceID: to.StringPtr(privateLinkResourceID),
					GroupIDs:             []*string{to.StringPtr(targetSubResource)},
				},
			}},
		},
	}

	pollersResp, err := client.BeginCreateOrUpdate(
		ctx,
		c.resourceGroupName,
		privateEndpointName,
		params,
		nil,
	)
	if err != nil {
		return nil, err
	}
	resp, err := pollersResp.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &resp.PrivateEndpoint, nil
}

func (c *Client) DeletePrivateEndpoint(ctx context.Context, privateEndpointName string) error {
	client, err := armnetwork.NewPrivateEndpointsClient(
		c.subscriptionID,
		c.creds,
		c.clientOpts,
	)
	if err != nil {
		return fmt.Errorf("failed to get private endpoints client: %q", err)
	}
	pollersResp, err := client.BeginDelete(
		ctx,
		c.resourceGroupName,
		privateEndpointName,
		nil,
	)
	if err != nil {
		return err
	}
	if _, err := pollersResp.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	return nil
}

// ConfigurePrivateDNS creates a private DNS zone, a record set (A) and a
// private DNS zone group for the given private endpoint.
// It also links the DNS zone with the given VNet by creating a virtual network
// link.
//
// Returns an error on failure.
func (c *Client) ConfigurePrivateDNS(
	ctx context.Context,
	privateEndpoint *armnetwork.PrivateEndpoint,
	vnetName,
	storageAccountName string,
) error {
	if err := c.createPrivateDNSZone(ctx, defaultPrivateZoneName, defaultPrivateZoneLocation); err != nil {
		return err
	}

	if err := c.createRecordSet(ctx, privateEndpoint, defaultPrivateZoneName, storageAccountName); err != nil {
		return err
	}

	if err := c.createPrivateDNSZoneGroup(ctx, *privateEndpoint.Name, defaultPrivateZoneName); err != nil {
		return err
	}

	if err := c.createVirtualNetworkLink(
		ctx,
		storageAccountName,
		vnetName,
		defaultPrivateZoneName,
		defaultPrivateZoneLocation,
	); err != nil {
		respErr, ok := err.(*azcore.ResponseError)
		// on conflict, azure api will not return a 409 so we need
		// to match for the string.
		if !ok || respErr.ErrorCode != "Conflict" {
			return err
		}
	}

	return nil
}

// DestroyPrivateDNS unlinks the private zone from the vnet.
//
// It is meant to be used as a clean-up for ConfigurePrivateDNS. It will not
// undo everything ConfigurePrivateDNS does because it's difficult to know
// whether they are used by other components. We remove the resources we know
// for sure that the registry is the only one using.
func (c *Client) DestroyPrivateDNS(ctx context.Context, privateEndpointName, vnetName, storageAccountName string) error {
	if err := c.deleteRecordSet(
		ctx, privateEndpointName, defaultPrivateZoneName,
	); err != nil {
		return err
	}
	if err := c.deletePrivateDNSZoneGroup(
		ctx, privateEndpointName, defaultPrivateZoneName,
	); err != nil {
		return err
	}
	if err := c.deleteVirtualNetworkLink(
		ctx, storageAccountName, defaultPrivateZoneName,
	); err != nil {
		return err
	}
	return nil
}

func (c *Client) createPrivateDNSZone(ctx context.Context, name, location string) error {
	client, err := armprivatedns.NewPrivateZonesClient(c.subscriptionID, c.creds, c.clientOpts)
	if err != nil {
		return err
	}
	pollersResp, err := client.BeginCreateOrUpdate(
		ctx,
		c.resourceGroupName,
		name,
		armprivatedns.PrivateZone{
			Location: to.StringPtr(location),
			Tags:     c.tagset,
		},
		nil,
	)
	if err != nil {
		return err
	}
	if _, err := pollersResp.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	return nil
}

func (c *Client) createRecordSet(
	ctx context.Context,
	privateEndpoint *armnetwork.PrivateEndpoint,
	privateZoneName,
	relativeRecordSetName string,
) error {
	client, err := armprivatedns.NewRecordSetsClient(c.subscriptionID, c.creds, c.clientOpts)
	if err != nil {
		return fmt.Errorf("failed to get record sets client: %s", err)
	}

	nicAddress, err := c.getNICAddress(ctx, privateEndpoint)
	if err != nil {
		return err
	}

	rs := armprivatedns.RecordSet{
		Properties: &armprivatedns.RecordSetProperties{
			TTL: to.Int64Ptr(defaultRecordSetTTL),
			ARecords: []*armprivatedns.ARecord{{
				IPv4Address: to.StringPtr(nicAddress),
			}},
		},
	}

	if _, err := client.CreateOrUpdate(
		ctx,
		c.resourceGroupName,
		privateZoneName,
		armprivatedns.RecordTypeA,
		relativeRecordSetName,
		rs,
		nil,
	); err != nil {
		return err
	}

	return nil
}

func (c *Client) deleteRecordSet(ctx context.Context, privateZoneName, relativeRecordSetName string) error {
	client, err := armprivatedns.NewRecordSetsClient(c.subscriptionID, c.creds, c.clientOpts)
	if err != nil {
		return fmt.Errorf("failed to get record sets client: %s", err)
	}
	if _, err := client.Delete(
		ctx,
		c.resourceGroupName,
		privateZoneName,
		armprivatedns.RecordTypeA,
		relativeRecordSetName,
		nil,
	); err != nil {
		return err
	}

	return nil
}

func (c *Client) createPrivateDNSZoneGroup(ctx context.Context, privateEndpointName, privateZoneName string) error {
	client, err := armnetwork.NewPrivateDNSZoneGroupsClient(c.subscriptionID, c.creds, c.clientOpts)
	if err != nil {
		return fmt.Errorf("failed to get private dns zone groups client: %q", err)
	}
	privateZoneID := formatPrivateDNSZoneID(c.subscriptionID, c.resourceGroupName, privateZoneName)
	groupName := strings.Replace(privateZoneName, ".", "-", -1)
	group := armnetwork.PrivateDNSZoneGroup{
		Name: to.StringPtr(fmt.Sprintf("%s/default", privateZoneName)),
		Properties: &armnetwork.PrivateDNSZoneGroupPropertiesFormat{
			PrivateDNSZoneConfigs: []*armnetwork.PrivateDNSZoneConfig{{
				Name: to.StringPtr(groupName),
				Properties: &armnetwork.PrivateDNSZonePropertiesFormat{
					PrivateDNSZoneID: to.StringPtr(privateZoneID),
				},
			}},
		},
	}
	pollersResp, err := client.BeginCreateOrUpdate(
		ctx,
		c.resourceGroupName,
		privateEndpointName,
		groupName,
		group,
		nil,
	)
	if err != nil {
		return err
	}
	if _, err := pollersResp.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	return nil
}

func (c *Client) deletePrivateDNSZoneGroup(ctx context.Context, privateEndpointName, privateZoneName string) error {
	client, err := armnetwork.NewPrivateDNSZoneGroupsClient(c.subscriptionID, c.creds, c.clientOpts)
	if err != nil {
		return fmt.Errorf("failed to get private dns zone groups client: %q", err)
	}
	groupName := strings.Replace(privateZoneName, ".", "-", -1)
	pollersResp, err := client.BeginDelete(
		ctx,
		c.resourceGroupName,
		privateEndpointName,
		groupName,
		nil,
	)
	if err != nil {
		return err
	}
	if _, err := pollersResp.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	return nil
}

func (c *Client) createVirtualNetworkLink(ctx context.Context, linkName, vnetName, privateZoneName, privateZoneLocation string) error {
	client, err := armprivatedns.NewVirtualNetworkLinksClient(c.subscriptionID, c.creds, c.clientOpts)
	if err != nil {
		return fmt.Errorf("failed to get virtual network links client: %s", err)
	}

	vnetID := formatVNetID(c.subscriptionID, c.resourceGroupName, vnetName)

	pollersResp, err := client.BeginCreateOrUpdate(
		ctx,
		c.resourceGroupName,
		privateZoneName,
		linkName,
		armprivatedns.VirtualNetworkLink{
			Location: to.StringPtr(privateZoneLocation),
			Tags:     c.tagset,
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

	if _, err := pollersResp.PollUntilDone(ctx, nil); err != nil {
		return err
	}

	return nil
}

func (c *Client) deleteVirtualNetworkLink(ctx context.Context, linkName, privateZoneName string) error {
	client, err := armprivatedns.NewVirtualNetworkLinksClient(c.subscriptionID, c.creds, c.clientOpts)
	if err != nil {
		return fmt.Errorf("failed to get virtual network links client: %s", err)
	}

	pollersResp, err := client.BeginDelete(
		ctx,
		c.resourceGroupName,
		privateZoneName,
		linkName,
		nil,
	)
	if err != nil {
		return err
	}
	if _, err := pollersResp.PollUntilDone(ctx, nil); err != nil {
		return err
	}
	return nil
}

func (c *Client) getNICAddress(ctx context.Context, privateEndpoint *armnetwork.PrivateEndpoint) (string, error) {
	client, err := armnetwork.NewInterfacesClient(c.subscriptionID, c.creds, c.clientOpts)
	if err != nil {
		return "", err
	}

	if len(privateEndpoint.Properties.NetworkInterfaces) == 0 {
		return "", fmt.Errorf("private endpoint %s did not have any network interfaces", *privateEndpoint.Name)
	}
	// this is auto-created by Azure and there should always ever be one.
	nicRef := privateEndpoint.Properties.NetworkInterfaces[0]
	nicIDParts := strings.Split(*nicRef.ID, "/")
	nicName := nicIDParts[len(nicIDParts)-1]

	resp, err := client.Get(ctx, c.resourceGroupName, nicName, nil)
	if err != nil {
		return "", err
	}
	nic := resp.Interface
	if len(nic.Properties.IPConfigurations) == 0 {
		return "", fmt.Errorf("network interface %s did not have any IP configurations", *nic.Name)
	}

	// this is auto-created by Azure and there should always ever be one.
	nicAddress := nic.Properties.IPConfigurations[0].Properties.PrivateIPAddress

	return *nicAddress, nil
}

func formatSubnetID(subnetName, vnetName, resourceGroupName, subscriptionID string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s/subnets/%s",
		subscriptionID,
		resourceGroupName,
		vnetName,
		subnetName,
	)
}

func formatPrivateLinkResourceID(subscriptionID, resourceGroupName, storageAccountName string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s",
		subscriptionID,
		resourceGroupName,
		storageAccountName,
	)
}

func formatPrivateDNSZoneID(subscriptionID, resourceGroupName, privateZoneName string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/privateDnsZones/%s",
		subscriptionID,
		resourceGroupName,
		privateZoneName,
	)
}

func formatVNetID(subscriptionID, resourceGroupName, vnetName string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/virtualNetworks/%s",
		subscriptionID,
		resourceGroupName,
		vnetName,
	)
}

func validate(opts *Options) error {
	missingOpts := []string{}
	if opts.Environment.ResourceManagerEndpoint == "" {
		missingOpts = append(missingOpts, "'Environment.ResourceManagerEndpoint'")
	}
	if opts.Environment.ActiveDirectoryEndpoint == "" {
		missingOpts = append(missingOpts, "'Environment.ActiveDirectoryEndpoint'")
	}
	if opts.Environment.TokenAudience == "" {
		missingOpts = append(missingOpts, "'Environment.TokenAudience'")
	}
	if opts.TenantID == "" {
		missingOpts = append(missingOpts, "'TenantID'")
	}
	if opts.ClientID == "" {
		missingOpts = append(missingOpts, "'ClientID'")
	}
	if opts.ClientSecret == "" && opts.FederatedTokenFile == "" && opts.Creds == nil {
		missingOpts = append(
			missingOpts,
			[]string{"'ClientSecret'", "'FederatedTokenFile'", "'Creds'"}...,
		)
	}
	if opts.SubscriptionID == "" {
		missingOpts = append(missingOpts, "'SubscriptionID'")
	}
	if opts.ResourceGroupName == "" {
		missingOpts = append(missingOpts, "'ResourceGroupName'")
	}
	if len(missingOpts) > 0 {
		missing := strings.Join(missingOpts, ", ")
		return fmt.Errorf("client misconfigured, missing %s option(s)", missing)
	}
	return nil
}
