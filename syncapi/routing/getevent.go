// Copyright 2022 The Matrix.org Foundation C.I.C.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package routing

import (
	"net/http"

	"github.com/matrix-org/util"
	"github.com/sirupsen/logrus"

	"github.com/matrix-org/dendrite/roomserver/api"
	"github.com/matrix-org/dendrite/setup/config"
	"github.com/matrix-org/dendrite/syncapi/internal"
	"github.com/matrix-org/dendrite/syncapi/storage"
	"github.com/matrix-org/dendrite/syncapi/synctypes"
	userapi "github.com/matrix-org/dendrite/userapi/api"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// GetEvent implements
//
//	GET /_matrix/client/r0/rooms/{roomId}/event/{eventId}
//
// https://spec.matrix.org/v1.4/client-server-api/#get_matrixclientv3roomsroomideventeventid
func GetEvent(
	req *http.Request,
	device *userapi.Device,
	roomID string,
	eventID string,
	cfg *config.SyncAPI,
	syncDB storage.Database,
	rsAPI api.SyncRoomserverAPI,
) util.JSONResponse {
	ctx := req.Context()
	db, err := syncDB.NewDatabaseTransaction(ctx)
	logger := util.GetLogger(ctx).WithFields(logrus.Fields{
		"event_id": eventID,
		"room_id":  roomID,
	})
	if err != nil {
		logger.WithError(err).Error("GetEvent: syncDB.NewDatabaseTransaction failed")
		return util.JSONResponse{
			Code: http.StatusInternalServerError,
			JSON: spec.InternalServerError{},
		}
	}

	events, err := db.Events(ctx, []string{eventID})
	if err != nil {
		logger.WithError(err).Error("GetEvent: syncDB.Events failed")
		return util.JSONResponse{
			Code: http.StatusInternalServerError,
			JSON: spec.InternalServerError{},
		}
	}

	// The requested event does not exist in our database
	if len(events) == 0 {
		logger.Debugf("GetEvent: requested event doesn't exist locally")
		return util.JSONResponse{
			Code: http.StatusNotFound,
			JSON: spec.NotFound("The event was not found or you do not have permission to read this event"),
		}
	}

	// If the request is coming from an appservice, get the user from the request
	userID := device.UserID
	if asUserID := req.FormValue("user_id"); device.AppserviceID != "" && asUserID != "" {
		userID = asUserID
	}

	// Apply history visibility to determine if the user is allowed to view the event
	events, err = internal.ApplyHistoryVisibilityFilter(ctx, db, rsAPI, events, nil, userID, "event")
	if err != nil {
		logger.WithError(err).Error("GetEvent: internal.ApplyHistoryVisibilityFilter failed")
		return util.JSONResponse{
			Code: http.StatusInternalServerError,
			JSON: spec.InternalServerError{},
		}
	}

	// We only ever expect there to be one event
	if len(events) != 1 {
		// 0 events -> not allowed to view event; > 1 events -> something that shouldn't happen
		logger.WithField("event_count", len(events)).Debug("GetEvent: can't return the requested event")
		return util.JSONResponse{
			Code: http.StatusNotFound,
			JSON: spec.NotFound("The event was not found or you do not have permission to read this event"),
		}
	}

	sender := spec.UserID{}
	senderUserID, err := rsAPI.QueryUserIDForSender(req.Context(), roomID, events[0].SenderID())
	if err == nil && senderUserID != nil {
		sender = *senderUserID
	}

	sk := events[0].StateKey()
	if sk != nil && *sk != "" {
		skUserID, err := rsAPI.QueryUserIDForSender(ctx, events[0].RoomID(), spec.SenderID(*events[0].StateKey()))
		if err == nil && skUserID != nil {
			skString := skUserID.String()
			sk = &skString
		}
	}
	return util.JSONResponse{
		Code: http.StatusOK,
		JSON: synctypes.ToClientEvent(events[0], synctypes.FormatAll, sender, sk),
	}
}
