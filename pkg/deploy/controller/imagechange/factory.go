package imagechange

import (
	"time"

	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/flowcontrol"
	utilruntime "k8s.io/kubernetes/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/watch"

	osclient "github.com/openshift/origin/pkg/client"
	controller "github.com/openshift/origin/pkg/controller"
	deployapi "github.com/openshift/origin/pkg/deploy/api"
	imageapi "github.com/openshift/origin/pkg/image/api"
)

// ImageChangeControllerFactory can create an ImageChangeController which
// watches all ImageStream changes.
type ImageChangeControllerFactory struct {
	// Client is an OpenShift client.
	Client osclient.Interface
}

// Create creates an ImageChangeController.
func (factory *ImageChangeControllerFactory) Create() controller.RunnableController {
	imageStreamLW := &cache.ListWatch{
		ListFunc: func(options kapi.ListOptions) (runtime.Object, error) {
			return factory.Client.ImageStreams(kapi.NamespaceAll).List(options)
		},
		WatchFunc: func(options kapi.ListOptions) (watch.Interface, error) {
			return factory.Client.ImageStreams(kapi.NamespaceAll).Watch(options)
		},
	}
	queue := cache.NewResyncableFIFO(cache.MetaNamespaceKeyFunc)
	cache.NewReflector(imageStreamLW, &imageapi.ImageStream{}, queue, 2*time.Minute).Run()

	deploymentConfigLW := &cache.ListWatch{
		ListFunc: func(options kapi.ListOptions) (runtime.Object, error) {
			return factory.Client.DeploymentConfigs(kapi.NamespaceAll).List(options)
		},
		WatchFunc: func(options kapi.ListOptions) (watch.Interface, error) {
			return factory.Client.DeploymentConfigs(kapi.NamespaceAll).Watch(options)
		},
	}
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	cache.NewReflector(deploymentConfigLW, &deployapi.DeploymentConfig{}, store, 2*time.Minute).Run()

	changeController := &ImageChangeController{
		listDeploymentConfigs: func() ([]*deployapi.DeploymentConfig, error) {
			configs := []*deployapi.DeploymentConfig{}
			objs := store.List()
			for _, obj := range objs {
				configs = append(configs, obj.(*deployapi.DeploymentConfig))
			}
			return configs, nil
		},
		client: factory.Client,
	}

	return &controller.RetryController{
		Queue: queue,
		RetryManager: controller.NewQueueRetryManager(
			queue,
			cache.MetaNamespaceKeyFunc,
			func(obj interface{}, err error, retries controller.Retry) bool {
				utilruntime.HandleError(err)
				if _, isFatal := err.(fatalError); isFatal {
					return false
				}
				if retries.Count > 2 {
					return false
				}
				return true
			},
			flowcontrol.NewTokenBucketRateLimiter(1, 10),
		),
		Handle: func(obj interface{}) error {
			repo := obj.(*imageapi.ImageStream)
			return changeController.Handle(repo)
		},
	}
}
