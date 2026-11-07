package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	chorePolicies "houseflowApi/internal/application/chore/policies"
	housePolicies "houseflowApi/internal/application/house/policies"
	storageAbstract "houseflowApi/internal/application/storage/abstract"
	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	maxChoreReviewPhotoSize = 3 * 1024 * 1024
	reviewPhotoCleanupLimit = 5 * time.Second
)

var supportedChoreReviewPhotoTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

type ChoreStatusUpdate struct {
	ChoreID string
	Status  entities.ChoreStatus
}

type ChoreReviewPhoto struct {
	Reader io.Reader
	Size   int64
}

type UpdateChoreStatusCommand struct {
	cqrs.Request[[]dtos.ChoreResponseModel]
	HouseID string
	Chores  []ChoreStatusUpdate
	UserID  string
	Photo   *ChoreReviewPhoto
}

type UpdateChoreStatusHandler struct {
	choreRepository         databaseAbstract.DbRepository[entities.Chore]
	statusHistoryRepository databaseAbstract.DbRepository[entities.ChoreStatusHistory]
	reviewVoteRepository    databaseAbstract.DbRepository[entities.ChoreReviewVote]
	membershipPolicy        *housePolicies.MembershipPolicy
	workflowPolicy          *chorePolicies.WorkflowPolicy
	objectStorage           storageAbstract.ObjectStorage
}

func NewUpdateChoreStatusHandler(
	choreRepository databaseAbstract.DbRepository[entities.Chore],
	statusHistoryRepository databaseAbstract.DbRepository[entities.ChoreStatusHistory],
	reviewVoteRepository databaseAbstract.DbRepository[entities.ChoreReviewVote],
	membershipPolicy *housePolicies.MembershipPolicy,
	workflowPolicy *chorePolicies.WorkflowPolicy,
	objectStorage storageAbstract.ObjectStorage,
) *UpdateChoreStatusHandler {
	return &UpdateChoreStatusHandler{
		choreRepository:         choreRepository,
		statusHistoryRepository: statusHistoryRepository,
		reviewVoteRepository:    reviewVoteRepository,
		membershipPolicy:        membershipPolicy,
		workflowPolicy:          workflowPolicy,
		objectStorage:           objectStorage,
	}
}

func (h *UpdateChoreStatusHandler) Handle(ctx context.Context, command UpdateChoreStatusCommand) ([]dtos.ChoreResponseModel, error) {
	if len(command.Chores) == 0 {
		return []dtos.ChoreResponseModel{}, nil
	}

	choreIDs := make(map[string]struct{}, len(command.Chores))
	for _, update := range command.Chores {
		if _, exists := choreIDs[update.ChoreID]; exists {
			return nil, helpers.NewLocalizedError("chore.error.duplicate_chore_id", update.ChoreID)
		}
		choreIDs[update.ChoreID] = struct{}{}
	}
	if command.Photo != nil && (len(command.Chores) != 1 || command.Chores[0].Status != entities.InTest) {
		return nil, helpers.NewLocalizedError("chore.error.review_photo_single_transition_required")
	}

	uploadedObjectKey, err := h.uploadReviewPhoto(ctx, command)
	if err != nil {
		return nil, err
	}

	updatedChores := make([]entities.Chore, 0, len(command.Chores))
	err = h.choreRepository.WithinTransaction(ctx, func(txCtx mongo.SessionContext) error {
		updatedChores = updatedChores[:0]
		house, err := h.membershipPolicy.RequireMember(txCtx, command.HouseID, command.UserID)
		if err != nil {
			return err
		}

		for _, updateRequest := range command.Chores {
			choreObjectID, err := helpers.ToMongoId(updateRequest.ChoreID)
			if err != nil {
				return helpers.NewLocalizedError("chore.error.invalid_chore_id", updateRequest.ChoreID)
			}
			currentChore, err := h.choreRepository.FindByID(txCtx, choreObjectID)
			if err != nil {
				return helpers.NewLocalizedError("chore.error.not_found", updateRequest.ChoreID)
			}
			if currentChore.HouseId != command.HouseID {
				return helpers.NewLocalizedError("chore.error.not_in_house", updateRequest.ChoreID)
			}
			now := time.Now()
			failedChore, failed, err := failChoreIfDue(
				txCtx, h.choreRepository, h.statusHistoryRepository, h.workflowPolicy, currentChore, now,
			)
			if err != nil {
				return err
			}
			if failed {
				updatedChores = append(updatedChores, *failedChore)
				continue
			}
			if updateRequest.Status == entities.InTest && house.IsChoreReviewPhotoRequired && command.Photo == nil {
				return helpers.NewLocalizedError("chore.error.review_photo_required")
			}

			previousStatus := currentChore.Status
			previousVersion := currentChore.Version
			if err := h.workflowPolicy.Advance(currentChore, updateRequest.Status, len(house.MemberIds), command.UserID, now); err != nil {
				return err
			}

			updateFields := bson.M{
				"status": currentChore.Status, "isCompleted": currentChore.IsCompleted,
				"completedBy": currentChore.CompletedBy, "completedAt": currentChore.CompletedAt,
				"reviewRound": currentChore.ReviewRound,
			}
			if updateRequest.Status == entities.InTest && uploadedObjectKey != "" {
				updateFields["reviewPhotoKeys."+strconv.Itoa(currentChore.ReviewRound)] = uploadedObjectKey
			}
			var updatedChore entities.Chore
			err = h.choreRepository.Collection().FindOneAndUpdate(txCtx, bson.M{
				"_id": choreObjectID, "version": previousVersion, "status": previousStatus,
			}, bson.M{"$set": updateFields, "$inc": bson.M{"version": 1}},
				options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&updatedChore)
			if err == mongo.ErrNoDocuments {
				return helpers.NewConflictError("chore.error.concurrent_update")
			}
			if err != nil {
				return err
			}
			if err := addStatusHistory(txCtx, h.statusHistoryRepository, updateRequest.ChoreID, updateRequest.Status, command.UserID, now); err != nil {
				return err
			}
			if previousStatus == entities.Progress && updatedChore.Status == entities.Completed {
				if err := addStatusHistory(txCtx, h.statusHistoryRepository, updateRequest.ChoreID, entities.Completed, command.UserID, now); err != nil {
					return err
				}
			}
			updatedChores = append(updatedChores, updatedChore)
		}
		return nil
	})
	if err != nil {
		if uploadedObjectKey != "" {
			if cleanupErr := h.deleteUploadedPhoto(uploadedObjectKey); cleanupErr != nil {
				return nil, errors.Join(err, cleanupErr)
			}
		}
		return nil, err
	}

	responses := make([]dtos.ChoreResponseModel, 0, len(updatedChores))
	for _, updatedChore := range updatedChores {
		histories, err := h.statusHistoryRepository.FindManyByColumn(ctx, "choreId", updatedChore.Id.Hex())
		if err != nil {
			return nil, err
		}
		votes, err := h.reviewVoteRepository.FindManyByColumn(ctx, "choreId", updatedChore.Id.Hex())
		if err != nil {
			return nil, err
		}
		responses = append(responses, dtos.ChoreToResponseModelWithReview(updatedChore, histories, votes))
	}
	return responses, nil
}

func (h *UpdateChoreStatusHandler) uploadReviewPhoto(ctx context.Context, command UpdateChoreStatusCommand) (string, error) {
	if command.Photo == nil {
		return "", nil
	}
	if h.objectStorage == nil {
		return "", helpers.NewUnavailableError("chore.error.review_photo_storage_unavailable", nil)
	}

	reader, contentType, extension, err := prepareChoreReviewPhoto(command.Photo)
	if err != nil {
		return "", err
	}

	house, err := h.membershipPolicy.RequireMember(ctx, command.HouseID, command.UserID)
	if err != nil {
		return "", err
	}
	update := command.Chores[0]
	choreObjectID, err := helpers.ToMongoId(update.ChoreID)
	if err != nil {
		return "", helpers.NewLocalizedError("chore.error.invalid_chore_id", update.ChoreID)
	}
	currentChore, err := h.choreRepository.FindByID(ctx, choreObjectID)
	if err != nil {
		return "", helpers.NewLocalizedError("chore.error.not_found", update.ChoreID)
	}
	if currentChore.HouseId != command.HouseID {
		return "", helpers.NewLocalizedError("chore.error.not_in_house", update.ChoreID)
	}
	now := time.Now()
	if h.workflowPolicy.ApplyDeadline(currentChore, now) {
		return "", helpers.NewLocalizedError("chore.error.invalid_status_transition")
	}
	if err := h.workflowPolicy.Advance(currentChore, update.Status, len(house.MemberIds), command.UserID, now); err != nil {
		return "", err
	}

	objectKey := fmt.Sprintf(
		"houses/%s/chores/%s/review-rounds/%d/%s%s",
		command.HouseID,
		update.ChoreID,
		currentChore.ReviewRound,
		primitive.NewObjectID().Hex(),
		extension,
	)
	if err := h.objectStorage.PutImage(ctx, objectKey, reader, command.Photo.Size, contentType); err != nil {
		return "", helpers.NewUnavailableError("chore.error.review_photo_upload_failed", err)
	}
	return objectKey, nil
}

func prepareChoreReviewPhoto(photo *ChoreReviewPhoto) (io.Reader, string, string, error) {
	if photo == nil || photo.Reader == nil || photo.Size <= 0 {
		return nil, "", "", helpers.NewLocalizedError("chore.error.review_photo_invalid")
	}
	if photo.Size > maxChoreReviewPhotoSize {
		return nil, "", "", helpers.NewLocalizedError("chore.error.review_photo_too_large")
	}

	headerSize := int64(512)
	if photo.Size < headerSize {
		headerSize = photo.Size
	}
	header := make([]byte, headerSize)
	read, err := io.ReadFull(photo.Reader, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, "", "", helpers.NewLocalizedError("chore.error.review_photo_invalid")
	}
	if read == 0 {
		return nil, "", "", helpers.NewLocalizedError("chore.error.review_photo_invalid")
	}
	header = header[:read]
	contentType := http.DetectContentType(header)
	extension, supported := supportedChoreReviewPhotoTypes[contentType]
	if !supported {
		return nil, "", "", helpers.NewLocalizedError("chore.error.review_photo_type_unsupported")
	}
	return io.MultiReader(bytes.NewReader(header), photo.Reader), contentType, extension, nil
}

func (h *UpdateChoreStatusHandler) deleteUploadedPhoto(objectKey string) error {
	if h.objectStorage == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), reviewPhotoCleanupLimit)
	defer cancel()
	if err := h.objectStorage.DeleteObject(cleanupCtx, objectKey); err != nil {
		return fmt.Errorf("delete uncommitted chore review photo: %w", err)
	}
	return nil
}

var _ cqrs.CommandHandler[UpdateChoreStatusCommand, []dtos.ChoreResponseModel] = (*UpdateChoreStatusHandler)(nil)
