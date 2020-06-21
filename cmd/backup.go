package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/pkg/errors"
	"github.com/rancher/norman/types/convert"
	//"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"io/ioutil"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	//"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	//"os"
)

func BackupCommand() cli.Command {
	backupFlags := []cli.Flag{
		formatFlag,
		cli.StringFlag{
			Name:  "kubeconfig",
			Usage: "Pass kubeconfig of cluster to be backed up",
		},
	}

	return cli.Command{
		Name:   "backup",
		Usage:  "Operations with backups",
		Action: defaultAction(backupCreate),
		Flags:  backupFlags,
		Subcommands: []cli.Command{
			cli.Command{
				Name:        "create",
				Usage:       "Perform backup/create snapshot",
				Description: "\nCreate a backup of Rancher MCM",
				ArgsUsage:   "None",
				Action:      backupCreate,
				Flags:       backupFlags,
			},
			cli.Command{
				Name:        "cluster",
				Usage:       "Backup entire cluster",
				Description: "\nCreate a backup of entire cluster",
				ArgsUsage:   "None",
				Action:      backupCluster,
				Flags:       backupFlags,
			},
		},
	}
}

func backupCreate(ctx *cli.Context) error {
	kubeconfig := ctx.String("kubeconfig")
	flag.Parse()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return err
	}
	clientSet, err := clientset.NewForConfig(config)
	if err != nil {
		return err
	}
	CRDs, err := clientSet.ApiextensionsV1beta1().CustomResourceDefinitions().List(v1.ListOptions{})
	if err != nil {
		return err
	}
	backupPath, err := ioutil.TempDir(".", "rancher-backup")
	if err != nil {
		return err
	}
	for _, crd := range CRDs.Items {
		group, version := crd.Spec.Group, crd.Spec.Versions[0].Name
		dyn, err := dynamic.NewForConfig(config)
		if err != nil {
			return err
		}
		var dr dynamic.ResourceInterface
		gvr := schema.GroupVersionResource{
			Group:    group,
			Version:  version,
			Resource: crd.Spec.Names.Plural,
		}
		dr = dyn.Resource(gvr)
		fmt.Printf("\ngvr: %v\n", gvr)
		cr, err := dr.List(v1.ListOptions{})
		if err != nil {
			return err
		}
		for _, item := range cr.Items {
			metadata := convert.ToMapInterface(item.Object["metadata"])
			delete(metadata, "creationTimestamp")
			delete(metadata, "resourceVersion")
			delete(metadata, "uid")
			item.Object["metadata"] = metadata
			writeToFile(item.Object, backupPath)
		}
	}

	return nil
}

// from velero https://github.com/vmware-tanzu/velero/blob/master/pkg/backup/item_collector.go#L267
func writeToFile(item map[string]interface{}, backupPath string) (string, error) {
	f, err := ioutil.TempFile(backupPath, "")
	if err != nil {
		return "", errors.Wrap(err, "error creating temp file")
	}
	defer f.Close()

	jsonBytes, err := json.Marshal(item)
	if err != nil {
		return "", errors.Wrap(err, "error converting item to JSON")
	}

	if _, err := f.Write(jsonBytes); err != nil {
		return "", errors.Wrap(err, "error writing JSON to file")
	}

	if err := f.Close(); err != nil {
		return "", errors.Wrap(err, "error closing file")
	}

	return f.Name(), nil
}

func canListResource(verbs v1.Verbs) bool {
	for _, v := range verbs {
		if v == "list" {
			return true
		}
	}
	return false
}

func backupCluster(ctx *cli.Context) error {
	kubeconfig := ctx.String("kubeconfig")
	flag.Parse()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return err
	}
	clientSet, err := clientset.NewForConfig(config)
	if err != nil {
		return err
	}
	discoveryClient := clientSet.Discovery()
	serverGroups, err := discoveryClient.ServerGroups()
	if err != nil {
		return err
	}
	//fmt.Printf("\napi groups supported by server: \n")
	backupResources := map[string]bool{"v1": true, "rbac.authorization.k8s.io/v1": true,
		"management.cattle.io/v3": true, "project.cattle.io/v3": true}

	for _, svGrp := range serverGroups.Groups {
		//fmt.Printf("%#v\n", svGrp)
		if backupResources[svGrp.Versions[0].GroupVersion] {
			if svGrp.Versions[0].GroupVersion == "v1" {
				fmt.Printf("\nsvGrp: %#v\n", svGrp)
			}
			resources, _ := discoveryClient.ServerResourcesForGroupVersion(svGrp.Versions[0].GroupVersion)
			fmt.Printf("\nresources in %v groupVersion:\n", svGrp.Versions[0].GroupVersion)
			for _, res := range resources.APIResources {
				if !canListResource(res.Verbs) {
					continue
				}
				fmt.Printf("%v\n", res.Name)
				fmt.Printf("res: %#v\n", res)
				dyn, err := dynamic.NewForConfig(config)
				if err != nil {
					return err
				}
				gv, err := schema.ParseGroupVersion(svGrp.Versions[0].GroupVersion)
				if err != nil {
					fmt.Printf("\nERROR parsing group version %v\n", err)
				}

				gvr := gv.WithResource(res.Name)
				fmt.Printf("\ngvr: %v\n", gvr)
				var dr dynamic.ResourceInterface
				dr = dyn.Resource(gvr)
				ress, err := dr.List(v1.ListOptions{})
				if err != nil {
					return err
				}
				fmt.Printf("\n")
				for _, rrr := range ress.Items {
					fmt.Printf("%v\n", rrr)
				}
			}
		}
	}
	return nil
}

type kubernetesResource struct {
	groupResource         schema.GroupResource
	preferredGVR          schema.GroupVersionResource
	namespace, name, path string
}

// getAllItems gets all relevant items from all API groups.
//func getAllItems() []*kubernetesResource {
//	var resources []*kubernetesResource
//	for _, group := range r.discoveryHelper.Resources() {
//		groupItems, err := r.getGroupItems(r.log, group)
//		if err != nil {
//			r.log.WithError(err).WithField("apiGroup", group.String()).Error("Error collecting resources from API group")
//			continue
//		}
//
//		resources = append(resources, groupItems...)
//	}
//
//	return resources
//}
//
//// getGroupItems collects all relevant items from a single API group.
//func (r *itemCollector) getGroupItems(log logrus.FieldLogger, group *metav1.APIResourceList) ([]*kubernetesResource, error) {
//	log = log.WithField("group", group.GroupVersion)
//
//	log.Infof("Getting items for group")
//
//	// Parse so we can check if this is the core group
//	gv, err := schema.ParseGroupVersion(group.GroupVersion)
//	if err != nil {
//		return nil, errors.Wrapf(err, "error parsing GroupVersion %q", group.GroupVersion)
//	}
//	if gv.Group == "" {
//		// This is the core group, so make sure we process in the following order: pods, pvcs, pvs,
//		// everything else.
//		sortCoreGroup(group)
//	}
//
//	var items []*kubernetesResource
//	for _, resource := range group.APIResources {
//		resourceItems, err := r.getResourceItems(log, gv, resource)
//		if err != nil {
//			log.WithError(err).WithField("resource", resource.String()).Error("Error getting items for resource")
//			continue
//		}
//
//		items = append(items, resourceItems...)
//	}
//
//	return items, nil
//}
//
//// getResourceItems collects all relevant items for a given group-version-resource.
//func getResourceItems(log logrus.FieldLogger, gv schema.GroupVersion, resource metav1.APIResource) ([]*kubernetesResource, error) {
//
//	log.Info("Getting items for resource")
//
//	var (
//		gvr           = gv.WithResource(resource.Name)
//		gr            = gvr.GroupResource()
//		clusterScoped = !resource.Namespaced
//	)
//
//	// Getting the preferred group version of this resource
//	preferredGVR, _, err := r.discoveryHelper.ResourceFor(gr.WithVersion(""))
//	if err != nil {
//		return nil, errors.WithStack(err)
//	}
//
//	// If we get here, we're backing up something other than namespaces
//	var namespacesToList []string
//	if clusterScoped {
//		namespacesToList = []string{""}
//	}
//
//	var items []*kubernetesResource
//
//	for _, namespace := range namespacesToList {
//		log = log.WithField("namespace", namespace)
//
//		resourceClient, err := r.dynamicFactory.ClientForGroupVersionResource(gv, resource, namespace)
//		if err != nil {
//			log.WithError(err).Error("Error getting dynamic client")
//			continue
//		}
//
//		var labelSelector string
//		if selector := r.backupRequest.Spec.LabelSelector; selector != nil {
//			labelSelector = metav1.FormatLabelSelector(selector)
//		}
//
//		log.Info("Listing items")
//		unstructuredList, err := resourceClient.List(metav1.ListOptions{LabelSelector: labelSelector})
//		if err != nil {
//			log.WithError(errors.WithStack(err)).Error("Error listing items")
//			continue
//		}
//		log.Infof("Retrieved %d items", len(unstructuredList.Items))
//
//		// collect the items
//		for i := range unstructuredList.Items {
//			item := &unstructuredList.Items[i]
//
//			if gr == kuberesource.Namespaces && !r.backupRequest.NamespaceIncludesExcludes.ShouldInclude(item.GetName()) {
//				log.WithField("name", item.GetName()).Info("Skipping namespace because it's excluded")
//				continue
//			}
//
//			path, err := r.writeToFile(item)
//			if err != nil {
//				log.WithError(err).Error("Error writing item to file")
//				continue
//			}
//
//			items = append(items, &kubernetesResource{
//				groupResource: gr,
//				preferredGVR:  preferredGVR,
//				namespace:     item.GetNamespace(),
//				name:          item.GetName(),
//				path:          path,
//			})
//		}
//	}
//
//	return items, nil
//}
