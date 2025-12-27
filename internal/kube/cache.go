package kube

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	discoveryinformers "k8s.io/client-go/informers/discovery/v1"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	discoverylisters "k8s.io/client-go/listers/discovery/v1"
	"k8s.io/client-go/tools/cache"
)

// ClusterCache wraps shared informers for the core resources we care about.
type ClusterCache struct {
	factory           informers.SharedInformerFactory
	nodeInformer      coreinformers.NodeInformer
	namespaceInformer coreinformers.NamespaceInformer
	podInformer       coreinformers.PodInformer
	serviceInformer   coreinformers.ServiceInformer
	endpointInformer  discoveryinformers.EndpointSliceInformer
	synced            []cache.InformerSynced
}

// NewClusterCache builds informers for nodes, namespaces, and pods.
func NewClusterCache(client kubernetes.Interface, resyncPeriod time.Duration) *ClusterCache {
	factory := informers.NewSharedInformerFactory(client, resyncPeriod)
	nodeInformer := factory.Core().V1().Nodes()
	namespaceInformer := factory.Core().V1().Namespaces()
	podInformer := factory.Core().V1().Pods()
	serviceInformer := factory.Core().V1().Services()
	endpointInformer := factory.Discovery().V1().EndpointSlices()

	return &ClusterCache{
		factory:           factory,
		nodeInformer:      nodeInformer,
		namespaceInformer: namespaceInformer,
		podInformer:       podInformer,
		serviceInformer:   serviceInformer,
		endpointInformer:  endpointInformer,
		synced: []cache.InformerSynced{
			nodeInformer.Informer().HasSynced,
			namespaceInformer.Informer().HasSynced,
			podInformer.Informer().HasSynced,
			serviceInformer.Informer().HasSynced,
			endpointInformer.Informer().HasSynced,
		},
	}
}

// Start launches the informers and waits for their caches to sync.
func (c *ClusterCache) Start(ctx context.Context) error {
	stopCh := ctx.Done()
	c.factory.Start(stopCh)
	if !cache.WaitForCacheSync(stopCh, c.synced...) {
		return fmt.Errorf("timed out waiting for informer caches to sync")
	}
	return nil
}

// NodeLister exposes the cached node lister.
func (c *ClusterCache) NodeLister() corev1listers.NodeLister {
	return c.nodeInformer.Lister()
}

// NamespaceLister exposes the cached namespace lister.
func (c *ClusterCache) NamespaceLister() corev1listers.NamespaceLister {
	return c.namespaceInformer.Lister()
}

// PodLister exposes the cached pod lister.
func (c *ClusterCache) PodLister() corev1listers.PodLister {
	return c.podInformer.Lister()
}

// ServiceLister exposes the cached service lister.
func (c *ClusterCache) ServiceLister() corev1listers.ServiceLister {
	return c.serviceInformer.Lister()
}

// EndpointsLister exposes the cached endpoints lister.
func (c *ClusterCache) EndpointsLister() discoverylisters.EndpointSliceLister {
	return c.endpointInformer.Lister()
}
