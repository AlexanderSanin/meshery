package resolver

import (
	"context"

	"github.com/layer5io/meshery/internal/graphql/model"
	"github.com/layer5io/meshkit/broker"
	"github.com/layer5io/meshkit/database"
	"github.com/layer5io/meshkit/utils"
	mesherykube "github.com/layer5io/meshkit/utils/kubernetes"
	meshsyncmodel "github.com/layer5io/meshsync/pkg/model"
)

var (
	meshsyncName    = "meshsync"
	meshsyncSubject = "meshery.>"
	meshsyncQueue   = "meshery"
	meshsyncYaml    = "https://raw.githubusercontent.com/layer5io/meshery-operator/master/config/samples/meshery_v1alpha1_meshsync.yaml"
)

func (r *Resolver) getMeshSyncStatus(ctx context.Context) (*model.OperatorControllerStatus, error) {
	return nil, nil
}

func (r *Resolver) listenToMeshSyncEvents(ctx context.Context) (<-chan *model.OperatorControllerStatus, error) {
	channel := make(chan *model.OperatorControllerStatus)
	status := model.StatusUnknown

	go func(ch chan *model.OperatorControllerStatus) {
		err := listernToEvents(r.DBHandler, r.meshsyncChannel)
		if err != nil {
			r.Log.Error(err)
			ch <- &model.OperatorControllerStatus{
				Name:   &meshsyncName,
				Status: &status,
				Error: &model.Error{
					Code:        "",
					Description: err.Error(),
				},
			}
			return
		}
		// extension to notify other channel when data comes in
	}(channel)

	return channel, nil
}

func runMeshSync(client *mesherykube.Client, delete bool) error {
	err := applyYaml(client, delete, meshsyncYaml)
	if err != nil {
		return err
	}
	return nil
}

func recordMeshSyncData(handler *database.Handler, object meshsyncmodel.Object) error {
	result := handler.Create(&object)
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func listernToEvents(handler *database.Handler, datach chan *broker.Message) error {
	for {
		select {
		case msg := <-datach:
			objectJSON, _ := utils.Marshal(msg.Object)
			object := meshsyncmodel.Object{}
			err := utils.Unmarshal(string(objectJSON), &object)
			if err != nil {
				return err
			}

			// persist the object
			err = recordMeshSyncData(handler, object)
			if err != nil {
				return err
			}
		}
	}
}
