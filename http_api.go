package main

import (
	"denkit-stash/auth"
	"denkit-stash/handlers"
	"denkit-stash/models"
	"net/http"
	"reflect"
	"regexp"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humamux"
)

const apiKeySecurity = "ApiKeyAuth"

var pathParamPattern = regexp.MustCompile(`\{([^}]+)\}`)

type errorResponse struct {
	Errors []string `json:"errors"`
}

type statusResponse struct {
	Message string `json:"message" example:"DenKit Stash"`
	Version string `json:"version" example:"1.0.0"`
}

type publicUserResponse struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type profileResponse struct {
	User publicUserResponse `json:"user"`
}

type gameResponse struct {
	ID             int64              `json:"id"`
	Title          string             `json:"title"`
	ShortText      string             `json:"short_text"`
	Type           string             `json:"type"`
	Classification string             `json:"classification"`
	URL            string             `json:"url"`
	User           publicUserResponse `json:"user"`
}

type gameListResponse struct {
	Games []gameResponse `json:"games"`
}

type gameEnvelopeResponse struct {
	Game gameResponse `json:"game"`
}

type coreUploadResponse struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	DisplayName string `json:"display_name"`
	Size        int64  `json:"size"`
	Storage     string `json:"storage"`
	Type        string `json:"type"`
	Platforms   string `json:"platforms" doc:"JSON array encoded as a string."`
}

type uploadListResponse struct {
	Uploads []coreUploadResponse `json:"uploads"`
}

type uploadEnvelopeResponse struct {
	Upload coreUploadResponse `json:"upload"`
}

type coreBuildResponse struct {
	ID            int64  `json:"id"`
	UploadID      int64  `json:"upload_id,omitempty"`
	UserVersion   string `json:"user_version"`
	State         string `json:"state"`
	ParentBuildID int64  `json:"parent_build_id,omitempty"`
	CreatedAt     string `json:"created_at"`
}

type buildListResponse struct {
	Builds []coreBuildResponse `json:"builds"`
}

type buildEnvelopeResponse struct {
	Build coreBuildResponse `json:"build"`
}

type downloadSessionResponse struct {
	UUID string `json:"uuid" format:"uuid"`
}

type wharfStatusResponse struct {
	OK bool `json:"ok" example:"true"`
}

type wharfBuildFileResponse struct {
	ID        int64  `json:"id"`
	Size      int64  `json:"size"`
	State     string `json:"state" enum:"uploading,uploaded,failed"`
	Type      string `json:"type" enum:"archive,patch,signature"`
	SubType   string `json:"subType" example:"default"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type wharfBuildResponse struct {
	ID            int64                    `json:"id"`
	UploadID      int64                    `json:"uploadId"`
	ParentBuildID int64                    `json:"parentBuildId"`
	Version       int64                    `json:"version"`
	State         string                   `json:"state" enum:"started,processing,completed,failed"`
	UserVersion   string                   `json:"userVersion"`
	Files         []wharfBuildFileResponse `json:"files"`
	CreatedAt     string                   `json:"createdAt"`
	UpdatedAt     string                   `json:"updatedAt"`
}

type wharfUploadRefResponse struct {
	ID int64 `json:"id"`
}

type wharfChannelResponse struct {
	Name   string                 `json:"name"`
	Upload wharfUploadRefResponse `json:"upload"`
	Head   *wharfBuildResponse    `json:"head,omitempty"`
}

type channelEnvelopeResponse struct {
	Channel wharfChannelResponse `json:"channel"`
}

type channelMapResponse struct {
	Channels map[string]wharfChannelResponse `json:"channels"`
}

type createBuildRequest struct {
	Target      string `json:"target" form:"target" example:"username/game" required:"true"`
	Channel     string `json:"channel" form:"channel" example:"main" required:"true"`
	UserVersion string `json:"user_version,omitempty" form:"user_version"`
}

type createBuildFileRequest struct {
	Type       string `json:"type" form:"type" example:"patch" enum:"archive,patch,signature" required:"true"`
	SubType    string `json:"sub_type" form:"sub_type" example:"default"`
	UploadType string `json:"upload_type" form:"upload_type" example:"deferred_resumable" enum:"deferred_resumable,deferred-resumable"`
}

type buildFileUploadResponse struct {
	ID            int64             `json:"id"`
	UploadURL     string            `json:"uploadUrl" format:"uri"`
	UploadParams  map[string]string `json:"uploadParams"`
	UploadHeaders map[string]string `json:"uploadHeaders"`
}

type buildFileUploadEnvelopeResponse struct {
	File buildFileUploadResponse `json:"file"`
}

type finalizeBuildFileRequest struct {
	Size int64 `json:"size" form:"size"`
}

type finalizedBuildFileResponse struct {
	ID    int64  `json:"id"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

type finalizedBuildFileEnvelopeResponse struct {
	File finalizedBuildFileResponse `json:"file"`
}

type buildFilesResponse struct {
	Files []wharfBuildFileResponse `json:"files"`
}

type upgradePathResponse struct {
	UpgradePath struct {
		Builds []wharfBuildResponse `json:"builds"`
	} `json:"upgradePath"`
}

type storageTestResponse struct {
	Message     string `json:"message"`
	SignedURL   string `json:"signed_url" format:"uri"`
	ExpiresIn   string `json:"expires_in"`
	TestContent string `json:"test_content"`
}

func newAPIConfig() huma.Config {
	config := huma.DefaultConfig("DenKit Stash API", "1.0.0")
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		apiKeySecurity: {
			Type:        "apiKey",
			In:          "header",
			Name:        "Authorization",
			Description: "Raw API key, Bearer token, or access_token token.",
		},
	}
	return config
}

func registerDenKitAPI(api huma.API, db models.Database, coreHandlers *handlers.CoreHandlers, wharfHandlers *handlers.WharfHandlers) {
	registerRaw[statusResponse](api, huma.Operation{
		OperationID: "get-status",
		Method:      http.MethodGet,
		Path:        "/",
		Tags:        []string{"Status"},
		Summary:     "Get service status",
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":"DenKit Stash","version":"1.0.0"}`))
	}))

	registerCoreRoutes(api, db, coreHandlers)
	registerWharfRoutes(api, db, wharfHandlers)
}

func registerCoreRoutes(api huma.API, db models.Database, coreHandlers *handlers.CoreHandlers) {
	registerRaw[profileResponse](api, authOperation("get-profile", http.MethodGet, "/profile", "Get the authenticated user profile", "Core", 401), authHandler(db, coreHandlers.GetProfile))
	registerRaw[gameListResponse](api, authOperation("list-profile-games", http.MethodGet, "/profile/games", "List games owned by the authenticated user", "Core", 401), authHandler(db, coreHandlers.GetProfileGames))
	registerRaw[gameEnvelopeResponse](api, publicOperation("get-game", http.MethodGet, "/games/{id}", "Get a game by ID", "Core", 400, 404), optionalAuthHandler(db, coreHandlers.GetGame))
	registerRaw[uploadListResponse](api, publicOperation("list-game-uploads", http.MethodGet, "/games/{id}/uploads", "List uploads for a game", "Core", 400, 404), optionalAuthHandler(db, coreHandlers.GetGameUploads))
	registerRaw[downloadSessionResponse](api, publicOperation("create-download-session", http.MethodPost, "/games/{id}/download-sessions", "Create a download session UUID", "Core", 400, 404), optionalAuthHandler(db, coreHandlers.CreateDownloadSession))
	registerRaw[uploadEnvelopeResponse](api, publicOperation("get-upload", http.MethodGet, "/uploads/{id}", "Get an upload by ID", "Core", 400, 404), optionalAuthHandler(db, coreHandlers.GetUpload))
	registerRaw[buildListResponse](api, publicOperation("list-upload-builds", http.MethodGet, "/uploads/{id}/builds", "List builds for an upload", "Core", 400, 404), optionalAuthHandler(db, coreHandlers.GetUploadBuilds))
	registerRaw[buildEnvelopeResponse](api, publicOperation("get-build", http.MethodGet, "/builds/{id}", "Get a build by ID", "Core", 400, 404), optionalAuthHandler(db, coreHandlers.GetBuild))
}

func registerWharfRoutes(api huma.API, db models.Database, wharfHandlers *handlers.WharfHandlers) {
	registerRaw[wharfStatusResponse](api, authOperation("get-wharf-status", http.MethodGet, "/wharf/status", "Check Wharf-compatible API status", "Wharf", 401), authHandler(db, wharfHandlers.GetWharfStatus))
	registerRaw[channelMapResponse](api, authOperation("list-wharf-channels", http.MethodGet, "/wharf/channels", "List channels for a target game", "Wharf", 400, 401, 403, 404), authHandler(db, wharfHandlers.ListChannels), queryParam("target", "Target in username/game form", true))
	registerRaw[channelEnvelopeResponse](api, authOperation("get-wharf-channel", http.MethodGet, "/wharf/channels/{channel}", "Get one channel for a target game", "Wharf", 400, 401, 403, 404), authHandler(db, wharfHandlers.GetChannel), queryParam("target", "Target in username/game form", true))
	registerRaw[buildListResponse](api, authOperation("list-wharf-builds", http.MethodGet, "/wharf/builds", "List builds for a target channel", "Wharf", 400, 401, 403, 404), authHandler(db, wharfHandlers.ListBuilds), queryParam("target", "Target in username/game form", true), queryParam("channel", "Channel name", true))
	registerRaw[buildEnvelopeResponse](api, authOperation("get-latest-completed-wharf-build", http.MethodGet, "/wharf/builds/latest", "Get newest completed build for a target channel and user version", "Wharf", 400, 401, 403, 404), authHandler(db, wharfHandlers.GetLatestCompletedBuild), queryParam("target", "Target in username/game form", true), queryParam("channel", "Channel name", true), queryParam("user_version", "Build user version", true))
	registerRaw[buildEnvelopeResponse](api, authOperation("create-wharf-build", http.MethodPost, "/wharf/builds", "Create a build for a target channel", "Wharf", 400, 401, 403, 404, 413), authHandler(db, wharfHandlers.CreateBuild), requestBody[createBuildRequest]())
	registerRaw[buildFilesResponse](api, authOperation("list-wharf-build-files", http.MethodGet, "/wharf/builds/{id}/files", "List files for a build", "Wharf", 400, 401, 403, 404), authHandler(db, wharfHandlers.GetBuildFiles))
	registerRaw[buildFileUploadEnvelopeResponse](api, authOperation("create-wharf-build-file", http.MethodPost, "/wharf/builds/{id}/files", "Create a build file upload", "Wharf", 400, 401, 403, 404, 413), authHandler(db, wharfHandlers.CreateBuildFile), requestBody[createBuildFileRequest]())
	registerRaw[finalizedBuildFileEnvelopeResponse](api, authOperation("finalize-wharf-build-file", http.MethodPost, "/wharf/builds/{buildId}/files/{fileId}", "Finalize a build file after upload", "Wharf", 400, 401, 403, 404, 413), authHandler(db, wharfHandlers.FinalizeBuildFile), optionalRequestBody[finalizeBuildFileRequest]())

	downloadResponses := map[int]reflect.Type{307: reflect.TypeOf("")}
	registerRawOperation(api, authOperation("download-wharf-build-file", http.MethodGet, "/wharf/builds/{buildId}/files/{fileId}/download", "Redirect to a build file download URL", "Wharf", 400, 401, 403, 404), authHandler(db, wharfHandlers.GetBuildFileDownload), downloadResponses)
	registerRawOperation(api, authOperation("head-wharf-build-file", http.MethodHead, "/wharf/builds/{buildId}/files/{fileId}/download", "Redirect to a build file download URL", "Wharf", 401, 403), authHandler(db, wharfHandlers.GetBuildFileDownload), downloadResponses)

	registerRawOperation(api, noSecurityOperation("start-upload-session", http.MethodPost, "/wharf/upload-sessions/{id}", "Start a deferred resumable upload session", "Wharf", 404), wharfHandlers.StartUploadSession, map[int]reflect.Type{201: reflect.TypeOf("")})
	registerRawOperation(api, noSecurityOperation("put-upload-session", http.MethodPut, "/wharf/upload-sessions/{id}", "Upload bytes to a deferred resumable upload session", "Wharf", 400, 404, 413), wharfHandlers.PutUploadSession, map[int]reflect.Type{200: reflect.TypeOf(""), 308: reflect.TypeOf("")}, headerParam("Content-Range", "GCS-style upload content range", true))

	download := auth.AuthMiddleware(db)(http.HandlerFunc(wharfHandlers.GetBuildDownloadByType))
	registerRawOperation(api, authOperation("download-build-artifact", http.MethodGet, "/builds/{buildId}/download/{type}/{subType}", "Redirect to a build artifact by type and subtype", "Downloads", 400, 401, 403, 404), download.ServeHTTP, downloadResponses)
	registerRawOperation(api, authOperation("head-build-artifact", http.MethodHead, "/builds/{buildId}/download/{type}/{subType}", "Redirect to a build artifact by type and subtype", "Downloads", 401), download.ServeHTTP, downloadResponses)

	upgradePath := auth.AuthMiddleware(db)(http.HandlerFunc(wharfHandlers.GetUpgradePath))
	registerRaw[upgradePathResponse](api, authOperation("get-upgrade-path", http.MethodGet, "/builds/{installedBuildId}/upgrade-paths/{targetBuildId}", "Resolve patch/signature upgrade path between builds", "Downloads", 400, 401, 403, 404), upgradePath.ServeHTTP)

	latestArchive := auth.AuthMiddleware(db)(http.HandlerFunc(wharfHandlers.GetLatestChannelArchive))
	registerRawOperation(api, authOperation("download-latest-channel-archive", http.MethodGet, "/{namespace}/{game}/{channel}/archive/default", "Redirect to the latest archive for a channel", "Downloads", 401, 403, 404), latestArchive.ServeHTTP, downloadResponses)
	registerRawOperation(api, authOperation("head-latest-channel-archive", http.MethodHead, "/{namespace}/{game}/{channel}/archive/default", "Redirect to the latest archive for a channel", "Downloads", 401), latestArchive.ServeHTTP, downloadResponses)
}

func registerRaw[T any](api huma.API, op huma.Operation, handler http.HandlerFunc, opts ...func(*huma.Operation, huma.API)) {
	registerRawOperation(api, op, handler, map[int]reflect.Type{200: reflect.TypeFor[T]()}, opts...)
}

func registerRawOperation(api huma.API, op huma.Operation, handler http.HandlerFunc, responses map[int]reflect.Type, opts ...func(*huma.Operation, huma.API)) {
	op.Responses = map[string]*huma.Response{}
	for status, typ := range responses {
		op.Responses[statusCode(status)] = responseFor(api, status, typ)
	}
	for _, status := range op.Errors {
		op.Responses[statusCode(status)] = responseFor(api, status, reflect.TypeFor[errorResponse]())
	}
	for _, opt := range opts {
		opt(&op, api)
	}
	api.OpenAPI().AddOperation(&op)
	api.Adapter().Handle(&op, func(ctx huma.Context) {
		if handler == nil {
			ctx.SetStatus(http.StatusNotImplemented)
			return
		}
		r, w := humamux.Unwrap(ctx)
		handler(w, r)
	})
}

func authHandler(db models.Database, handler http.HandlerFunc) http.HandlerFunc {
	if db == nil || handler == nil {
		return nil
	}
	return auth.AuthMiddleware(db)(handler).ServeHTTP
}

func optionalAuthHandler(db models.Database, handler http.HandlerFunc) http.HandlerFunc {
	if db == nil || handler == nil {
		return nil
	}
	return auth.OptionalAuthMiddleware(db)(handler).ServeHTTP
}

func responseFor(api huma.API, status int, typ reflect.Type) *huma.Response {
	response := &huma.Response{Description: http.StatusText(status)}
	if typ == reflect.TypeOf("") {
		response.Headers = map[string]*huma.Param{
			"Location": {Schema: &huma.Schema{Type: "string", Format: "uri"}},
		}
		return response
	}
	response.Content = map[string]*huma.MediaType{
		"application/json": {
			Schema: huma.SchemaFromType(api.OpenAPI().Components.Schemas, typ),
		},
	}
	return response
}

func requestBody[T any]() func(*huma.Operation, huma.API) {
	return func(op *huma.Operation, api huma.API) {
		op.RequestBody = &huma.RequestBody{
			Required: true,
			Content: map[string]*huma.MediaType{
				"application/json": {
					Schema: huma.SchemaFromType(api.OpenAPI().Components.Schemas, reflect.TypeFor[T]()),
				},
				"application/x-www-form-urlencoded": {
					Schema: huma.SchemaFromType(api.OpenAPI().Components.Schemas, reflect.TypeFor[T]()),
				},
			},
		}
	}
}

func optionalRequestBody[T any]() func(*huma.Operation, huma.API) {
	return func(op *huma.Operation, api huma.API) {
		requestBody[T]()(op, api)
		op.RequestBody.Required = false
	}
}

func queryParam(name string, description string, required bool) func(*huma.Operation, huma.API) {
	return func(op *huma.Operation, api huma.API) {
		op.Parameters = append(op.Parameters, &huma.Param{
			Name:        name,
			In:          "query",
			Description: description,
			Required:    required,
			Schema:      &huma.Schema{Type: "string"},
		})
	}
}

func headerParam(name string, description string, required bool) func(*huma.Operation, huma.API) {
	return func(op *huma.Operation, api huma.API) {
		op.Parameters = append(op.Parameters, &huma.Param{
			Name:        name,
			In:          "header",
			Description: description,
			Required:    required,
			Schema:      &huma.Schema{Type: "string"},
		})
	}
}

func authOperation(id string, method string, path string, summary string, tag string, errors ...int) huma.Operation {
	op := noSecurityOperation(id, method, path, summary, tag, errors...)
	op.Security = []map[string][]string{{apiKeySecurity: []string{}}}
	return op
}

func publicOperation(id string, method string, path string, summary string, tag string, errors ...int) huma.Operation {
	op := noSecurityOperation(id, method, path, summary, tag, errors...)
	op.Security = []map[string][]string{{apiKeySecurity: []string{}}, {}}
	return op
}

func noSecurityOperation(id string, method string, path string, summary string, tag string, errors ...int) huma.Operation {
	return huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Tags:        []string{tag},
		Summary:     summary,
		Errors:      errors,
		Parameters:  pathParams(path),
	}
}

func pathParams(path string) []*huma.Param {
	matches := pathParamPattern.FindAllStringSubmatch(path, -1)
	params := make([]*huma.Param, 0, len(matches))
	for _, match := range matches {
		name := match[1]
		schema := &huma.Schema{Type: "string"}
		if isIntegerPathParam(name) {
			schema = &huma.Schema{Type: "integer", Format: "int64"}
		}
		params = append(params, &huma.Param{
			Name:     name,
			In:       "path",
			Required: true,
			Schema:   schema,
		})
	}
	return params
}

func isIntegerPathParam(name string) bool {
	switch name {
	case "id", "buildId", "fileId", "installedBuildId", "targetBuildId":
		return true
	default:
		return false
	}
}

func statusCode(status int) string {
	return strconv.Itoa(status)
}
