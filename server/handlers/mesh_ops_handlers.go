// Package handlers :  collection of handlers (aka "HTTP middleware")
package handlers

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/layer5io/meshery/server/meshes"
	"github.com/layer5io/meshery/server/models"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func init() {
	gob.Register([]*models.Adapter{})
}

// swagger:route GET /api/system/adapters/available SystemAPI idGetAvailableAdapters
// Handle GET request for available adapters
//
// Fetches and returns all the adapters available for deployment
// Responses:
//  200: systemAdaptersRespWrapper

// AdaptersHandler is used to fetch all the adapters
func (h *Handler) AvailableAdaptersHandler(w http.ResponseWriter, _ *http.Request) {
	err := json.NewEncoder(w).Encode(models.ListAvailableAdapters)
	if err != nil {
		obj := "data"
		h.log.Error(ErrMarshal(err, obj))
		http.Error(w, ErrMarshal(err, obj).Error(), http.StatusInternalServerError)
		return
	}
}

// swagger:route GET /api/system/adapters SystemAPI idGetSystemAdapters
// Handle GET request for adapters
//
// Fetches and returns all the adapters and ping adapters
// Responses:
//  200: systemAdaptersRespWrapper

// AdaptersHandler is used to fetch all the adapters
func (h *Handler) AdaptersHandler(w http.ResponseWriter, req *http.Request, prefObj *models.Preference, user *models.User, provider models.Provider) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// if adapter found in query user is trying to ping an adapter
	adapterLoc := req.URL.Query().Get("adapter")
	if adapterLoc != "" {
		logrus.Debug("adapter pinging")
		h.AdapterPingHandler(w, req, prefObj, user, provider)
		return
	}

	err := json.NewEncoder(w).Encode(h.config.AdapterTracker.GetAdapters(req.Context()))
	if err != nil {
		obj := "data"
		h.log.Error(ErrMarshal(err, obj))
		http.Error(w, ErrMarshal(err, obj).Error(), http.StatusInternalServerError)
		return
	}
}

// AdapterPingHandler is used to ping a given adapter
func (h *Handler) AdapterPingHandler(w http.ResponseWriter, req *http.Request, prefObj *models.Preference, _ *models.User, _ models.Provider) {
	meshAdapters := prefObj.MeshAdapters
	if meshAdapters == nil {
		meshAdapters = []*models.Adapter{}
	}

	adapterLoc := req.URL.Query().Get("adapter")
	h.log.Debug("Adapter URL to ping: ", adapterLoc)
	logrus.Debug("Adapter URL to ping: ", adapterLoc)

	var targetAdapter *models.Adapter
	for _, ad := range meshAdapters {
		if adapterLoc == ad.Host {
			targetAdapter = ad
			break
		}
	}

	if targetAdapter == nil {
		h.log.Error(ErrValidAdapter)
		http.Error(w, ErrValidAdapter.Error(), http.StatusBadRequest)
		return
	}

	if !checkAdapterPingability(targetAdapter) {
		// h.log.Error("Adapter ping failed")
		http.Error(w, "Adapter ping failed", http.StatusInternalServerError)
		return
	}

	_, _ = w.Write([]byte("{}"))
}

func checkAdapterPingability(adapter *models.Adapter) bool {
	// Custom logic to check if the adapter is pingable
	// For example, you can establish a TCP connection to the adapter's port
	address := fmt.Sprintf("localhost:%d", adapter.Port)
	conn, err := net.Dial("tcp", address)
	if err != nil {
		// Error occurred while establishing the TCP connection
		return false
	}
	defer conn.Close()

	// Successfully established the TCP connection
	return true
}

// swagger:route POST /api/system/adapter/manage SystemAPI idPostAdapterConfig
// Handle POST requests to persist adapter config
//
// Used to persist adapter config
// responses:
// 	200: mesheryAdaptersRespWrapper

// swagger:route DELETE /api/system/adapter/manage SystemAPI idDeleteAdapterConfig
// Handle DELETE requests to delete adapter config
//
// Used to delete adapter configuration
// responses:
// 	200:

// MeshAdapterConfigHandler is used to persist adapter config
func (h *Handler) MeshAdapterConfigHandler(w http.ResponseWriter, req *http.Request, prefObj *models.Preference, user *models.User, provider models.Provider) {
	meshAdapters := prefObj.MeshAdapters
	if meshAdapters == nil {
		meshAdapters = []*models.Adapter{}
	}
	var err error

	switch req.Method {
	case http.MethodPost:
		meshLocationURL := req.FormValue("meshLocationURL")

		h.log.Debug("meshLocationURL: ", meshLocationURL)
		if strings.TrimSpace(meshLocationURL) == "" {
			h.log.Error(ErrAddAdapter)
			http.Error(w, ErrAddAdapter.Error(), http.StatusBadRequest)
			return
		}
		meshAdapters, err = h.addAdapter(req.Context(), meshAdapters, prefObj, meshLocationURL, provider)
		if err != nil {
			// h.log.Error(ErrRetrieveData(err))
			http.Error(w, ErrRetrieveData(err).Error(), http.StatusInternalServerError)
			return // error is handled appropriately in the relevant method
		}
	case http.MethodDelete:
		meshAdapters, err = h.deleteAdapter(meshAdapters, w, req)
		if err != nil {
			return // error is handled appropriately in the relevant method
		}
	default:
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	prefObj.MeshAdapters = meshAdapters
	err = provider.RecordPreferences(req, user.UserID, prefObj)
	if err != nil {
		h.log.Error(ErrRecordPreferences(err))
		http.Error(w, ErrRecordPreferences(err).Error(), http.StatusInternalServerError)
		return
	}

	err = json.NewEncoder(w).Encode(meshAdapters)
	if err != nil {
		obj := "data"
		h.log.Error(ErrMarshal(err, obj))
		http.Error(w, ErrMarshal(err, obj).Error(), http.StatusInternalServerError)
		return
	}
}

func (h *Handler) addAdapter(ctx context.Context, meshAdapters []*models.Adapter, _ *models.Preference, meshLocationURL string, _ models.Provider) ([]*models.Adapter, error) {
	alreadyConfigured := false
	for _, adapter := range meshAdapters {
		if adapter.Host == meshLocationURL {
			alreadyConfigured = true
			break
		}
	}

	if alreadyConfigured {
		h.log.Debug("Adapter already configured...")
		return meshAdapters, nil
	}

	mClient, err := meshes.CreateClient(ctx, meshLocationURL)
	if err != nil {
		h.log.Error(ErrMeshClient)
		return meshAdapters, ErrMeshClient
	}
	h.log.Debug("created client for adapter: ", meshLocationURL)
	defer func() {
		_ = mClient.Close()
	}()
	respOps, err := mClient.MClient.SupportedOperations(ctx, &meshes.SupportedOperationsRequest{})
	if err != nil {
		h.log.Error(ErrRetrieveMeshData(err))
		return meshAdapters, err
	}
	h.log.Debug("retrieved supported ops for adapter: ", meshLocationURL)
	meshInfo, err := mClient.MClient.ComponentInfo(ctx, &meshes.ComponentInfoRequest{})
	if err != nil {
		h.log.Error(ErrRetrieveMeshData(err))
		return meshAdapters, err
	}

	adapter := &models.Adapter{
		Host:         meshLocationURL,
		Name:         meshInfo.Name,
		Version:      meshInfo.Version,
		GitCommitSHA: meshInfo.GitSha,
		Ops:          respOps.GetOps(),
		Port:         models.GetNextAvailablePort(),
	}

	// Set the initial availability to true
	adapter.Available = true

	// Start the background goroutine to check the availability periodically
	models.CheckAdapterAvailability(adapter)

	h.config.AdapterTracker.AddAdapter(ctx, *adapter)
	meshAdapters = append(meshAdapters, adapter)
	return meshAdapters, nil
}

func (h *Handler) deleteAdapter(meshAdapters []*models.Adapter, w http.ResponseWriter, req *http.Request) ([]*models.Adapter, error) {
	adapterLoc := req.URL.Query().Get("adapter")
	h.log.Debug("URL of adapter to be removed: ", adapterLoc)

	adaptersLen := len(meshAdapters)

	aID := -1
	for i, ad := range meshAdapters {
		if adapterLoc == ad.Host {
			aID = i
			break
		}
	}
	if aID < 0 {
		h.log.Error(ErrValidAdapter)
		http.Error(w, ErrValidAdapter.Error(), http.StatusBadRequest)
		return meshAdapters, ErrValidAdapter
	}

	newMeshAdapters := []*models.Adapter{}
	if aID == 0 {
		newMeshAdapters = meshAdapters[1:]
	} else if aID == adaptersLen-1 {
		newMeshAdapters = meshAdapters[:adaptersLen-1]
	} else {
		newMeshAdapters = append(meshAdapters[:aID], meshAdapters[aID+1:]...)
	}

	b, _ := json.Marshal(meshAdapters)
	h.log.Debug("Old adapters: ", string(b))
	b, _ = json.Marshal(newMeshAdapters)
	h.log.Debug("New adapters: ", string(b))

	return newMeshAdapters, nil
}

// swagger:route POST /api/system/adapter/operation SystemAPI idPostAdapterOperation
// Handle POST requests for Adapter Operations
//
// Used to send operations to the adapters
// responses:
// 	200:

// MeshOpsHandler is used to send operations to the adapters
func (h *Handler) MeshOpsHandler(w http.ResponseWriter, req *http.Request, prefObj *models.Preference, user *models.User, provider models.Provider) {
	if provider.GetProviderType() == models.RemoteProviderType {
		token, err := provider.GetProviderToken(req)
		if err == nil {
			viper.SetDefault("opt-token", token)
		}
	}

	meshAdapters := prefObj.MeshAdapters
	if meshAdapters == nil {
		meshAdapters = []*models.Adapter{}
	}

	adapterLoc := req.FormValue("adapter")
	h.log.Debug("Adapter URL to execute operations on: ", adapterLoc)

	aID := -1
	for i, ad := range meshAdapters {
		// Compare the entire adapter URL (host:port) with adapterLoc
		if adapterLoc == fmt.Sprintf("%s:%s", ad.Host, ad.Port) {
			aID = i
			break
		}
	}
	if aID < 0 {
		h.log.Error(ErrValidAdapter)
		http.Error(w, ErrValidAdapter.Error(), http.StatusBadRequest)
		return
	}

	opName := req.FormValue("query")
	customBody := req.FormValue("customBody")
	namespace := req.FormValue("namespace")
	deleteOp := req.FormValue("deleteOp")
	if namespace == "" {
		namespace = "default"
	}

	mk8sContexts, ok := req.Context().Value(models.KubeClustersKey).([]models.K8sContext)
	if !ok || len(mk8sContexts) == 0 {
		h.log.Error(ErrInvalidK8SConfig)
		http.Error(w, ErrInvalidK8SConfig.Error(), http.StatusBadRequest)
		return
	}

	var configs []string
	for _, c := range mk8sContexts {
		// Generate Kube Handler
		kc, err := c.GenerateKubeConfig()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		configs = append(configs, string(kc))
	}

	mClient, err := meshes.CreateClient(req.Context(), meshAdapters[aID].Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		_ = mClient.Close()
	}()

	operationID, err := uuid.NewV4()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_, err = mClient.MClient.ApplyOperation(req.Context(), &meshes.ApplyRuleRequest{
		OperationId: operationID.String(),
		OpName:      opName,
		Username:    user.UserID,
		Namespace:   namespace,
		CustomBody:  customBody,
		DeleteOp:    (deleteOp != ""),
		KubeConfigs: configs,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
