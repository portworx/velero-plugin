package snapshot

import (
	"crypto/tls"
	"fmt"
	apiclient "github.com/libopenstorage/openstorage/api/client"
	"github.com/libopenstorage/openstorage/pkg/auth"
	"github.com/libopenstorage/openstorage/volume/drivers/pwx"
	lsecrets "github.com/libopenstorage/secrets"
	k8s_secrets "github.com/libopenstorage/secrets/k8s"
	"google.golang.org/grpc"
	"time"

	"github.com/vmware-tanzu/velero/pkg/plugin/velero"
	volumeclient "github.com/libopenstorage/openstorage/api/client/volume"
	"github.com/libopenstorage/openstorage/volume"
	"github.com/pkg/errors"
	"github.com/portworx/sched-ops/k8s/core"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// serviceName is the name of the portworx service
	serviceName = "portworx-service"

	// defaultNamespace is the namespace where portworx is installed by default
	defaultNamespace = "kube-system"
	// defaultPort is the port where portworx is listening on by default
	defaultPort = "9001"

	// Config parameters
	configTypeKey = "type"
	pxNamespaceKey = "PX_NAMESPACE"
	pxSharedSecretKey = "PX_SHARED_SECRET"
	pxJwtIssuerKey    = "PX_JWT_ISSUER"

	typeLocal = "local"
	typeCloud = "cloud"
	pxDriverName = "pxd"
	uniqueID = "velero-portworx-plugin"
)

// Plugin for managing Portworx snapshots
type Plugin struct {
	Log    logrus.FieldLogger
	plugin velero.VolumeSnapshotter
	pxClient *portworxClient
}

type portworxGrpcConnection struct {
	conn        *grpc.ClientConn
	dialOptions []grpc.DialOption
	endpoint    string
}

type portworxClient struct {
	namespace string
	jwtIssuer string
	jwtSharedSecret string
	pxEndpoint string
	sdkConn         *portworxGrpcConnection
	tlsConfig       *tls.Config
}

// Init the plugin
func (p *Plugin) Init(config map[string]string) error {
	if p.pxClient == nil {
		p.pxClient = &portworxClient{}
	}
	if namespace, ok := config[pxNamespaceKey]; ok && len(namespace) > 0 {
		p.pxClient.namespace = namespace
	} else {
		p.pxClient.namespace = defaultNamespace
	}

	if pxSharedSecret, ok := config[pxSharedSecretKey]; ok && len(pxSharedSecret) > 0 {
		p.pxClient.jwtSharedSecret = pxSharedSecret
	}

	if pxJwtIssuer, ok := config[pxJwtIssuerKey]; ok && len(pxJwtIssuer) > 0 {
		p.pxClient.jwtIssuer = pxJwtIssuer
	}
	p.Log.Infof("Initializing portworx client")
	if err := p.pxClient.initPortworxClients(); err != nil {
		p.Log.Errorf("Failed to init portworx clients: %v", err)
		return err
	}
	p.Log.Infof("Init'ing portworx plugin with config %v", config)

	if snapType, ok := config[configTypeKey]; !ok || snapType == typeLocal {
		p.plugin = &localSnapshotPlugin{log: p.Log, pxClient: p.pxClient}
	} else if snapType == typeCloud {
		p.plugin = &cloudSnapshotPlugin{log: p.Log, pxClient: p.pxClient}
	} else {
		err := fmt.Errorf("Snapshot type %v not supported", snapType)
		p.Log.Errorf("%v", err)
		return err
	}

	return p.plugin.Init(config)
}

// CreateVolumeFromSnapshot Create a volume form given snapshot
func (p *Plugin) CreateVolumeFromSnapshot(snapshotID, volumeType, volumeAZ string, iops *int64) (string, error) {
	return p.plugin.CreateVolumeFromSnapshot(snapshotID, volumeType, volumeAZ, iops)
}

// GetVolumeInfo Get information about the volume
func (p *Plugin) GetVolumeInfo(volumeID, volumeAZ string) (string, *int64, error) {
	return p.plugin.GetVolumeInfo(volumeID, volumeAZ)
}

// CreateSnapshot Create a snapshot
func (p *Plugin) CreateSnapshot(volumeID, volumeAZ string, tags map[string]string) (string, error) {
	return p.plugin.CreateSnapshot(volumeID, volumeAZ, tags)
}

// DeleteSnapshot Delete a snapshot
func (p *Plugin) DeleteSnapshot(snapshotID string) error {
	return p.plugin.DeleteSnapshot(snapshotID)
}

// GetVolumeID Get the volume ID from the spec
func (p *Plugin) GetVolumeID(unstructuredPV runtime.Unstructured) (string, error) {
	pv := new(v1.PersistentVolume)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredPV.UnstructuredContent(), pv); err != nil {
		return "", errors.WithStack(err)
	}
	if pv.Spec.CSI != nil {
		driver := pv.Spec.CSI.Driver
		if driver == pxdDriverName {
			return pv.Spec.CSI.VolumeHandle, nil
		}
		logrus.Infof("Unable to handle CSI driver: %s", driver)
	}

	if pv.Spec.PortworxVolume != nil {
		if pv.Spec.PortworxVolume.VolumeID == "" {
			return "", errors.New("portworx volumeID not found")
		}
		return pv.Spec.PortworxVolume.VolumeID, nil
	}

	return "", nil
}

// SetVolumeID Set the volume ID in the spec
func (p *Plugin) SetVolumeID(unstructuredPV runtime.Unstructured, volumeID string) (runtime.Unstructured, error) {
	pv := new(v1.PersistentVolume)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredPV.UnstructuredContent(), pv); err != nil {
		return nil, errors.WithStack(err)
	}
	if pv.Spec.CSI != nil {
		// PV is provisioned by CSI driver
		driver := pv.Spec.CSI.Driver
		if driver == pxdDriverName {
			pv.Spec.CSI.VolumeHandle = volumeID
		} else {
			return nil, fmt.Errorf("unable to handle CSI driver: %s", driver)
		}
	} else if pv.Spec.PortworxVolume != nil {
		// PV is provisioned by in-tree driver
		pv.Spec.PortworxVolume.VolumeID = volumeID
		pv.Name = volumeID
	} else {
		return nil, errors.New("spec.csi and spec.portworxVolume not found")
	}

	res, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pv)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return &unstructured.Unstructured{Object: res}, nil
}

func (p *portworxClient) getVolumeDriver() (volume.VolumeDriver, error) {
	if len(p.jwtSharedSecret) != 0 {
		claims := &auth.Claims{
			Issuer: p.jwtIssuer,
			Name:   "Stork",

			// Unique id for stork
			// this id must be unique across all accounts accessing the px system
			Subject: p.jwtIssuer + "." + uniqueID,

			// Only allow certain calls
			Roles: []string{"system.user"},

			// Be in all groups to have access to all resources
			Groups: []string{"*"},
		}

		// This never returns an error, but just in case, check the value
		signature, err := auth.NewSignatureSharedSecret(p.jwtSharedSecret)
		if err != nil {
			return nil, err
		}

		// Set the token expiration
		options := &auth.Options{
			Expiration:  time.Now().Add(time.Hour).Unix(),
			IATSubtract: 1 * time.Minute,
		}

		token, err := auth.Token(claims, signature, options)
		if err != nil {
			return nil, err
		}

		clnt, err := p.getRestClientWithAuth(token)
		if err != nil {
			return nil, err
		}
		return volumeclient.VolumeDriver(clnt), nil
	}

	clnt, err := p.getRestClient()
	if err != nil {
		return nil, err
	}
	return volumeclient.VolumeDriver(clnt), nil
}

func (p *portworxClient) getRestClientWithAuth(token string) (*apiclient.Client, error) {
	restClient, err := volumeclient.NewAuthDriverClient(p.pxEndpoint, pxDriverName, "", token, "", "stork")
	if err == nil && p.tlsConfig != nil {
		restClient.SetTLS(p.tlsConfig)
	}
	return restClient, err
}

func (p *portworxClient) getRestClient() (*apiclient.Client, error) {
	restClient, err := volumeclient.NewDriverClient(p.pxEndpoint, pxDriverName, "", "stork")
	if err == nil && p.tlsConfig != nil {
		restClient.SetTLS(p.tlsConfig)
	}
	return restClient, err
}

func (p *portworxClient) initPortworxClients() error {
	kubeOps := core.Instance()

	pbc := pwx.NewConnectionParamsBuilderDefaultConfig()

	if len(p.jwtSharedSecret) > 0 {
		pbc.AuthTokenGenerator = p.tokenGenerator
		pbc.AuthEnabled = true
	}

	paramsBuilder, err := pwx.NewConnectionParamsBuilder(kubeOps, pbc)
	if err != nil {
		return err
	}

	pxMgmtEndpoint, sdkEndpoint, err := paramsBuilder.BuildClientsEndpoints()
	if err != nil {
		return err
	}

	p.pxEndpoint = pxMgmtEndpoint
	p.tlsConfig, err = paramsBuilder.BuildTlsConfig()
	if err != nil {
		return err
	}

	sdkDialOps, err := paramsBuilder.BuildDialOps()
	if err != nil {
		return err
	}

	// Setup gRPC clients
	p.sdkConn = &portworxGrpcConnection{
		endpoint:    sdkEndpoint,
		dialOptions: sdkDialOps,
	}

	// Setup secrets instance
	k8sSecrets, err := k8s_secrets.New(nil)
	if err != nil {
		return fmt.Errorf("failed to initialize secrets provider: %v", err)
	}
	err = lsecrets.SetInstance(k8sSecrets)
	if err != nil {
		return fmt.Errorf("failed to set secrets provider: %v", err)
	}

	return err
}

// 	tokenGenerator generates authorization token for system.admin
//	when shared secret is not configured authz token is empty string
//	this let openstorage API clients be bootstrapped with no authorization (by accepting empty token)
func (p *portworxClient) tokenGenerator() (string, error) {
	if len(p.jwtSharedSecret) == 0 {
		return "", nil
	}

	claims := &auth.Claims{
		Issuer: p.jwtIssuer,
		Name:   "Stork",

		// Unique id for stork
		// this id must be unique across all accounts accessing the px system
		Subject: p.jwtIssuer + "." + uniqueID,

		// Only allow certain calls
		Roles: []string{"system.admin"},

		// Be in all groups to have access to all resources
		Groups: []string{"*"},
	}

	// This never returns an error, but just in case, check the value
	signature, err := auth.NewSignatureSharedSecret(p.jwtSharedSecret)
	if err != nil {
		return "", err
	}

	// Set the token expiration
	options := &auth.Options{
		Expiration:  time.Now().Add(time.Hour * 1).Unix(),
		IATSubtract: 1 * time.Minute,
	}

	token, err := auth.Token(claims, signature, options)
	if err != nil {
		return "", err
	}

	return token, nil
}
