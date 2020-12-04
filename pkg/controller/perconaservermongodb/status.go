package perconaservermongodb

import (
	"context"
	"fmt"
	"strings"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const maxStatusesQuantity = 20

type mongoClusterState int

const (
	clusterReady mongoClusterState = iota
	clusterInit
	clusterError
)

func (r *ReconcilePerconaServerMongoDB) updateStatus(cr *api.PerconaServerMongoDB, reconcileErr error, clusterState mongoClusterState) error {
	clusterCondition := api.ClusterCondition{
		Status:             api.ConditionTrue,
		Type:               api.ClusterInit,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
	if reconcileErr != nil {
		if cr.Status.State != api.AppStateError {
			clusterCondition = api.ClusterCondition{
				Status:             api.ConditionTrue,
				Type:               api.ClusterError,
				Message:            reconcileErr.Error(),
				Reason:             "ErrorReconcile",
				LastTransitionTime: metav1.NewTime(time.Now()),
			}
			cr.Status.Conditions = append(cr.Status.Conditions, clusterCondition)
		}

		cr.Status.Message = "Error: " + reconcileErr.Error()
		cr.Status.State = api.AppStateError

		return r.writeStatus(cr)
	}

	cr.Status.Message = ""

	replsetsReady := 0
	inProgress := false

	repls := cr.Spec.Replsets
	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.ConfigsvrReplSet != nil {
		repls = append(repls, cr.Spec.Sharding.ConfigsvrReplSet)
	}

	for _, rs := range repls {
		status, err := r.rsStatus(rs, cr.Name, cr.Namespace)
		if err != nil {
			return errors.Wrapf(err, "get replset %v status", rs.Name)
		}

		currentRSstatus, ok := cr.Status.Replsets[rs.Name]
		if !ok {
			currentRSstatus = &api.ReplsetStatus{}
		}

		status.Initialized = currentRSstatus.Initialized
		status.AddedAsShard = currentRSstatus.AddedAsShard

		if status.Status == api.AppStateReady {
			replsetsReady++
		}

		if status.Status != currentRSstatus.Status {
			if status.Status == api.AppStateReady && currentRSstatus.Initialized {
				clusterCondition = api.ClusterCondition{
					Status:             api.ConditionTrue,
					Type:               api.ClusterRSReady,
					LastTransitionTime: metav1.NewTime(time.Now()),
				}
			}

			if status.Status == api.AppStateError {
				clusterCondition = api.ClusterCondition{
					Status:             api.ConditionTrue,
					Message:            rs.Name + ": " + status.Message,
					Reason:             "ErrorRS",
					Type:               api.ClusterError,
					LastTransitionTime: metav1.NewTime(time.Now()),
				}
			}
			cr.Status.Conditions = append(cr.Status.Conditions, clusterCondition)
		}
		cr.Status.Replsets[rs.Name] = &status
		if !inProgress {
			inProgress, err = r.upgradeInProgress(cr, rs.Name)
			if err != nil {
				return errors.Wrapf(err, "set upgradeInProgres")
			}
		}
	}

	cr.Status.State = api.AppStateInit
	if replsetsReady == len(repls) && clusterState == clusterReady {
		clusterCondition = api.ClusterCondition{
			Status:             api.ConditionTrue,
			Type:               api.ClusterReady,
			LastTransitionTime: metav1.NewTime(time.Now()),
		}
		cr.Status.State = api.AppStateReady
	} else if cr.Status.Conditions[len(cr.Status.Conditions)-1].Type != api.ClusterReady &&
		clusterState == clusterInit {
		clusterCondition = api.ClusterCondition{
			Status:             api.ConditionTrue,
			Type:               api.ClusterInit,
			LastTransitionTime: metav1.NewTime(time.Now()),
		}
		cr.Status.State = api.AppStateInit
	} else {
		clusterCondition = api.ClusterCondition{
			Status:             api.ConditionTrue,
			Type:               api.ClusterError,
			LastTransitionTime: metav1.NewTime(time.Now()),
		}
		cr.Status.State = api.AppStateError
	}

	if len(cr.Status.Conditions) == 0 {
		cr.Status.Conditions = append(cr.Status.Conditions, clusterCondition)
	} else {
		lastClusterCondition := cr.Status.Conditions[len(cr.Status.Conditions)-1]
		switch {
		case lastClusterCondition.Type != clusterCondition.Type:
			cr.Status.Conditions = append(cr.Status.Conditions, clusterCondition)
		default:
			cr.Status.Conditions[len(cr.Status.Conditions)-1] = lastClusterCondition
		}
	}

	if len(cr.Status.Conditions) > maxStatusesQuantity {
		cr.Status.Conditions = cr.Status.Conditions[len(cr.Status.Conditions)-maxStatusesQuantity:]
	}

	if inProgress {
		cr.Status.State = api.AppStateInit
	}

	cr.Status.ObservedGeneration = cr.ObjectMeta.Generation

	host, err := r.connectionEndpoint(cr)
	if err != nil {
		log.Error(err, "get psmdb connection endpoint")
	}
	cr.Status.Host = host

	return r.writeStatus(cr)
}

func (r *ReconcilePerconaServerMongoDB) upgradeInProgress(cr *api.PerconaServerMongoDB, rsName string) (bool, error) {
	sfsObj := &appsv1.StatefulSet{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name + "-" + rsName, Namespace: cr.Namespace}, sfsObj)
	if err != nil {
		return false, err
	}

	return sfsObj.Status.Replicas > sfsObj.Status.UpdatedReplicas, nil
}

func (r *ReconcilePerconaServerMongoDB) writeStatus(cr *api.PerconaServerMongoDB) error {
	err := r.client.Status().Update(context.TODO(), cr)
	if err != nil {
		// may be it's k8s v1.10 and erlier (e.g. oc3.9) that doesn't support status updates
		// so try to update whole CR
		err := r.client.Update(context.TODO(), cr)
		if err != nil {
			return errors.Wrap(err, "send update")
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) rsStatus(rsSpec *api.ReplsetSpec, clusterName, namespace string) (api.ReplsetStatus, error) {
	list := corev1.PodList{}
	err := r.client.List(context.TODO(),
		&list,
		&client.ListOptions{
			Namespace: namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/instance":   clusterName,
				"app.kubernetes.io/replset":    rsSpec.Name,
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
			}),
		},
	)
	if err != nil {
		return api.ReplsetStatus{}, fmt.Errorf("get list: %v", err)
	}

	status := api.ReplsetStatus{
		Size:   rsSpec.Size,
		Status: api.AppStateInit,
	}

	for _, pod := range list.Items {
		for _, cond := range pod.Status.Conditions {
			switch cond.Type {
			case corev1.ContainersReady:
				if cond.Status == corev1.ConditionTrue {
					status.Ready++
				} else if cond.Status == corev1.ConditionFalse {
					for _, cntr := range pod.Status.ContainerStatuses {
						if cntr.State.Waiting != nil && cntr.State.Waiting.Message != "" {
							status.Message += cntr.Name + ": " + cntr.State.Waiting.Message + "; "
						}
					}
				}
			case corev1.PodScheduled:
				if cond.Reason == corev1.PodReasonUnschedulable &&
					cond.LastTransitionTime.Time.Before(time.Now().Add(-1*time.Minute)) {
					status.Status = api.AppStateError
					status.Message = cond.Message
				}
			}
		}
	}

	if status.Size == status.Ready {
		status.Status = api.AppStateReady
	}

	return status, nil
}

func (r *ReconcilePerconaServerMongoDB) connectionEndpoint(cr *api.PerconaServerMongoDB) (string, error) {
	if cr.Spec.Sharding.Enabled {
		if mongos := cr.Spec.Sharding.Mongos; mongos.Expose.Enabled &&
			mongos.Expose.ExposeType == corev1.ServiceTypeLoadBalancer {
			return loadBalancerServiceEndpoint(r.client, cr.Name+"-mongos", cr.Namespace)
		}
		return cr.Name + "-mongos." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix, nil
	}

	if rs := cr.Spec.Replsets[0]; rs.Expose.Enabled &&
		rs.Expose.ExposeType == corev1.ServiceTypeLoadBalancer {
		list := corev1.PodList{}
		err := r.client.List(context.TODO(),
			&list,
			&client.ListOptions{
				Namespace: cr.Namespace,
				LabelSelector: labels.SelectorFromSet(map[string]string{
					"app.kubernetes.io/name":       "percona-server-mongodb",
					"app.kubernetes.io/instance":   cr.Name,
					"app.kubernetes.io/replset":    rs.Name,
					"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
					"app.kubernetes.io/part-of":    "percona-server-mongodb",
				}),
			},
		)
		if err != nil {
			return "", errors.Wrap(err, "list psmdb pods")
		}
		addrs, err := psmdb.GetReplsetAddrs(r.client, cr, rs, list.Items)
		if err != nil {
			return "", err
		}
		return strings.Join(addrs, ","), nil
	}

	return cr.Name + "-" + cr.Spec.Replsets[0].Name + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix, nil
}

func loadBalancerServiceEndpoint(client client.Client, serviceName, namespace string) (string, error) {
	host := ""
	srv := corev1.Service{}
	err := client.Get(context.TODO(), types.NamespacedName{
		Namespace: namespace,
		Name:      serviceName,
	}, &srv)
	if err != nil {
		return "", errors.Wrap(err, "get service")
	}
	for _, i := range srv.Status.LoadBalancer.Ingress {
		host = i.IP
		if len(i.Hostname) > 0 {
			host = i.Hostname
		}
	}
	return host, nil
}
