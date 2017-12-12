package controller

import (
	"log"

	"github.com/apprenda/kismatic/pkg/install"
	"github.com/apprenda/kismatic/pkg/provision"
	"github.com/apprenda/kismatic/pkg/store"
	"github.com/google/go-cmp/cmp"
)

const (
	planned         = "planned"
	provisioning    = "provisioning"
	provisionFailed = "provisionFailed"
	provisioned     = "provisioned"
	installing      = "installing"
	installFailed   = "installFailed"
	installed       = "installed"
	modifying       = "modifying"
	modifyFailed    = "modifyFailed"
	destroying      = "destroying"
	destroyFailed   = "destroyFailed"
	destroyed       = "destroyed"
)

// The clusterController manages the lifecycle of a single cluster.
type clusterController struct {
	clusterName    string
	clusterSpec    store.Cluster
	log            *log.Logger
	executor       install.Executor
	newProvisioner func(store.Cluster) provision.Provisioner
	clusterStore   store.ClusterStore
}

// This is the controller's reconciliation loop. It listens on a channel for
// changes to the cluster spec. In the case of a mismatch between the current
// state and the desired state, the controller will take action by transitioning
// the cluster towards the desired state.
func (c *clusterController) run(watch <-chan struct{}) {
	c.log.Printf("started controller for cluster %q", c.clusterName)
	for _ = range watch {
		cluster, err := c.clusterStore.Get(c.clusterName)
		if err != nil {
			c.log.Printf("error getting cluster from store: %v", err)
			continue
		}
		c.log.Printf("cluster %q - current state: %s, desired state: %s, can continue: %v", c.clusterName, cluster.CurrentState, cluster.DesiredState, cluster.CanContinue)

		// If the plan has changed, set the current state to planned.
		if !cmp.Equal(cluster.Plan, c.clusterSpec.Plan) {
			cluster.CurrentState = planned
		}

		// If we have reached the desired state, don't do anything. Similarly,
		// if we can't continue due to a failure, don't do anything.
		if cluster.CurrentState == cluster.DesiredState || !cluster.CanContinue {
			continue
		}

		// Transition the cluster to the next state
		updatedCluster := c.transition(*cluster)

		// Transitions are long - O(minutes). Get the latest cluster spec from
		// the store before updating it.
		// TODO: Ideally we would run this in a transaction, but the current
		// implementation of the store does not expose txs.
		cluster, err = c.clusterStore.Get(c.clusterName)
		if err != nil {
			c.log.Printf("error getting cluster from store: %v", err)
			continue
		}

		// Update a subset of the fields in the cluster spec.
		cluster.Plan = updatedCluster.Plan
		cluster.CurrentState = updatedCluster.CurrentState
		cluster.CanContinue = updatedCluster.CanContinue
		err = c.clusterStore.Put(c.clusterName, *cluster)
		if err != nil {
			c.log.Printf("error storing cluster state: %v. The cluster's current state is %q and desired state is %q", err, cluster.CurrentState, cluster.DesiredState)
			continue
		}

		// Update the controller's state of the world to the latest state.
		c.clusterSpec = *cluster

		// If the cluster has been destroyed, remove the cluster from the store
		// and stop the controller
		if cluster.CurrentState == destroyed {
			err := c.clusterStore.Delete(c.clusterName)
			if err != nil {
				// At this point, the cluster has already been destroyed, but we
				// failed to remove the cluster resource from the database. The
				// only thing that can be done is for the user to issue another
				// delete so that we try again.
				c.log.Printf("could not delete cluster %q from store: %v", c.clusterName, err)
				continue
			}
			c.log.Printf("cluster %q has been destroyed. stoppping controller.", c.clusterName)
			return
		}
	}
	c.log.Printf("stopping controller that was managing cluster %q", c.clusterName)
}

// transition performs an action to take the cluster to the next state. The
// action to be performed depends on the current state and the desired state.
// Once the action is done, an updated cluster spec is returned that reflects
// the outcome of the action.
func (c *clusterController) transition(cluster store.Cluster) store.Cluster {
	if cluster.CurrentState == cluster.DesiredState {
		return cluster
	}
	switch cluster.CurrentState {
	case planned:
		cluster.CurrentState = provisioning
		return cluster
	case provisioning:
		return c.provision(cluster)
	case provisioned:
		if cluster.DesiredState == destroyed {
			cluster.CurrentState = destroying
			return cluster
		}
		cluster.CurrentState = installing
		return cluster
	case provisionFailed:
		if cluster.DesiredState == destroyed {
			cluster.CurrentState = destroying
			return cluster
		}
		cluster.CurrentState = provisioning
		return cluster
	case destroying:
		return c.destroy(cluster)
	case installing:
		return c.install(cluster)
	case installFailed:
		if cluster.DesiredState == destroyed {
			cluster.CurrentState = destroying
			return cluster
		}
		cluster.CurrentState = installing
		return cluster
	case installed:
		if cluster.DesiredState == destroyed {
			cluster.CurrentState = destroying
			return cluster
		}
		c.log.Printf("cluster %q: cannot transition to %q from the 'installed' state", c.clusterName, cluster.DesiredState)
		cluster.CanContinue = false
		return cluster
	default:
		// Log a message, and set CanContinue to false so that we don't get
		// stuck in an infinte loop. The only thing the user can do in this case
		// is delete the cluster and file a bug, as this scenario should not
		// happen.
		c.log.Printf("cluster %q: the desired state is %q, but there is no transition defined for the cluster's current state %q", c.clusterName, cluster.DesiredState, cluster.CurrentState)
		cluster.CanContinue = false
		return cluster
	}
}

func (c *clusterController) provision(cluster store.Cluster) store.Cluster {
	c.log.Printf("provisioning infrastructure for cluster %q", c.clusterName)
	provisioner := c.newProvisioner(cluster)
	updatedPlan, err := provisioner.Provision(cluster.Plan)
	if err != nil {
		c.log.Printf("error provisioning infrastructure for cluster %q: %v", c.clusterName, err)
		cluster.CurrentState = provisionFailed
		cluster.CanContinue = false
		return cluster
	}
	cluster.Plan = *updatedPlan
	cluster.CurrentState = provisioned
	return cluster
}

func (c *clusterController) destroy(cluster store.Cluster) store.Cluster {
	c.log.Printf("destroying cluster %q", c.clusterName)
	provisioner := c.newProvisioner(cluster)
	err := provisioner.Destroy(cluster.Plan.Cluster.Name)
	if err != nil {
		c.log.Printf("error destroying cluster %q: %v", c.clusterName, err)
		cluster.CurrentState = destroyFailed
		cluster.CanContinue = false
		return cluster
	}
	cluster.CurrentState = destroyed
	return cluster
}

func (c *clusterController) install(cluster store.Cluster) store.Cluster {
	c.log.Printf("installing cluster %q", c.clusterName)
	plan := cluster.Plan

	err := c.executor.RunPreFlightCheck(&plan)
	if err != nil {
		c.log.Printf("cluster %q: error running preflight checks: %v", c.clusterName, err)
		cluster.CurrentState = installFailed
		cluster.CanContinue = false
		return cluster
	}

	err = c.executor.GenerateCertificates(&plan, false)
	if err != nil {
		c.log.Printf("cluster %q: error generating certificates: %v", c.clusterName, err)
		cluster.CurrentState = installFailed
		cluster.CanContinue = false
		return cluster
	}

	err = c.executor.GenerateKubeconfig(plan)
	if err != nil {
		c.log.Printf("cluster %q: error generating kubeconfig file: %v", c.clusterName, err)
		cluster.CurrentState = installFailed
		cluster.CanContinue = false
		return cluster
	}

	err = c.executor.Install(&plan, true)
	if err != nil {
		c.log.Printf("cluster %q: error installing the cluster: %v", c.clusterName, err)
		cluster.CurrentState = installFailed
		cluster.CanContinue = false
		return cluster
	}

	// Skip the smoketest if the user asked us to skip the installation of a
	// networking stack
	if !plan.NetworkConfigured() {
		cluster.CurrentState = installed
		return cluster
	}

	err = c.executor.RunSmokeTest(&plan)
	if err != nil {
		c.log.Printf("cluster %q: error running smoke test against the cluster: %v", c.clusterName, err)
		cluster.CurrentState = installFailed
		return cluster
	}

	cluster.CurrentState = installed
	return cluster
}
