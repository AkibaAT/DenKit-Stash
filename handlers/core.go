package handlers

import (
	"denkit-stash/auth"
	"denkit-stash/models"
	"net/http"

	"github.com/google/uuid"
)

type CoreHandlers struct {
	db models.Database
}

func NewCoreHandlers(db models.Database) *CoreHandlers {
	return &CoreHandlers{db: db}
}

func (h *CoreHandlers) GetProfile(w http.ResponseWriter, r *http.Request) {
	user := auth.MustGetUser(r.Context())
	writeJSON(w, http.StatusOK, ProfileResponse{User: PublicUserResponse{
		ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
	}})
}

func (h *CoreHandlers) GetProfileGames(w http.ResponseWriter, r *http.Request) {
	user := auth.MustGetUser(r.Context())
	games, err := h.db.GetGamesByUserID(user.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	var responseGames []ProfileGameResponse
	for _, game := range games {
		responseGames = append(responseGames, ProfileGameResponse{
			ID: game.ID, UserID: game.UserID, Title: game.Title, ShortText: game.ShortText,
			Type: game.Type, Classification: game.Classification, URL: game.URL,
			CreatedAt: game.CreatedAt, UpdatedAt: game.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, GameListResponse{Games: responseGames})
}

func (h *CoreHandlers) GetGame(w http.ResponseWriter, r *http.Request) {
	gameID, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}

	user, game, err := h.db.GetGameByID(gameID)
	if err != nil {
		writeError(w, http.StatusNotFound, "game not found")
		return
	}

	writeJSON(w, http.StatusOK, GameEnvelopeResponse{Game: GameResponse{
		ID: game.ID, Title: game.Title, ShortText: game.ShortText, Type: game.Type,
		Classification: game.Classification, URL: game.URL,
		User: PublicUserResponse{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName},
	}})
}

func (h *CoreHandlers) GetGameUploads(w http.ResponseWriter, r *http.Request) {
	gameID, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	if _, _, err = h.db.GetGameByID(gameID); err != nil {
		writeError(w, http.StatusNotFound, "game not found")
		return
	}

	uploads, err := h.db.GetUploadsByGameID(gameID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	var responseUploads []CoreUploadResponse
	for _, upload := range uploads {
		responseUploads = append(responseUploads, newCoreUploadResponse(upload))
	}
	writeJSON(w, http.StatusOK, UploadListResponse{Uploads: responseUploads})
}

func (h *CoreHandlers) CreateDownloadSession(w http.ResponseWriter, r *http.Request) {
	gameID, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	if _, _, err = h.db.GetGameByID(gameID); err != nil {
		writeError(w, http.StatusNotFound, "game not found")
		return
	}
	writeJSON(w, http.StatusOK, DownloadSessionResponse{UUID: uuid.New().String()})
}

func (h *CoreHandlers) GetUpload(w http.ResponseWriter, r *http.Request) {
	uploadID, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload id")
		return
	}
	upload, err := h.db.GetUploadByID(uploadID)
	if err != nil {
		writeError(w, http.StatusNotFound, "upload not found")
		return
	}
	writeJSON(w, http.StatusOK, UploadEnvelopeResponse{Upload: newCoreUploadResponse(upload)})
}

func (h *CoreHandlers) GetUploadBuilds(w http.ResponseWriter, r *http.Request) {
	uploadID, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload id")
		return
	}
	if _, err = h.db.GetUploadByID(uploadID); err != nil {
		writeError(w, http.StatusNotFound, "upload not found")
		return
	}

	builds, err := h.db.GetBuildsByUploadID(uploadID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	var responseBuilds []CoreBuildResponse
	for _, build := range builds {
		responseBuilds = append(responseBuilds, newCoreBuildResponse(build, false))
	}
	writeJSON(w, http.StatusOK, BuildListResponse{Builds: responseBuilds})
}

func (h *CoreHandlers) GetBuild(w http.ResponseWriter, r *http.Request) {
	buildID, err := pathInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid build id")
		return
	}
	build, err := h.db.GetBuildByID(buildID)
	if err != nil {
		writeError(w, http.StatusNotFound, "build not found")
		return
	}
	writeJSON(w, http.StatusOK, BuildEnvelopeResponse{Build: newCoreBuildResponse(build, true)})
}
