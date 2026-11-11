package policies

import (
	"context"
	"errors"
	"strconv"
	"time"

	databaseAbstract "houseflowApi/internal/data/database/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MaxCodeAttempts is how many codes, right or wrong, one email may try per
// purpose and code window.
const MaxCodeAttempts = 5

// Each flow counts on its own. Password reset and email verification mail the
// same code, so a reset must not use up the verification flow's code.
const (
	CodePurposeResetPassword = "reset-password"
	CodePurposeVerifyEmail   = "verify-email"
	CodePurposeSocialSignUp  = "social-signup"
)

// CodeCheck is one attempt at an emailed code.
type CodeCheck struct {
	Purpose string
	// Email is what the attempts are counted against.
	Email string
	// Subject is what the code was generated from.
	Subject         string
	Code            string
	Secret          string
	ValidityMinutes int
}

// CodeAttempts limits guesses at emailed codes. A code is a short HMAC of its
// subject and time window, so without a limit it could be guessed online.
type CodeAttempts struct {
	attemptRepository databaseAbstract.DbRepository[entities.AuthCodeAttempt]
}

func NewCodeAttempts(attemptRepository databaseAbstract.DbRepository[entities.AuthCodeAttempt]) *CodeAttempts {
	return &CodeAttempts{attemptRepository: attemptRepository}
}

// Verify reports whether check.Code is a live code. Every attempt, right or
// wrong, is counted first in one atomic update, so parallel requests cannot
// get more than MaxCodeAttempts codes compared per email and window.
func (p *CodeAttempts) Verify(ctx context.Context, check CodeCheck) (bool, error) {
	current := helpers.ResetCodeWindow(check.ValidityMinutes)
	attempts, err := p.raise(ctx, check, current, bson.M{"$inc": bson.M{"count": 1}})
	if err != nil || attempts > MaxCodeAttempts {
		return false, err
	}

	window, matched := helpers.MatchResetCode(check.Subject, check.Code, check.Secret, current, check.ValidityMinutes)
	if !matched || window.Equal(current) {
		return matched, nil
	}
	// A code from the previous window stays refused once that window was
	// used up or the code was consumed.
	var previous entities.AuthCodeAttempt
	err = p.attemptRepository.Collection().FindOne(ctx, bson.M{"_id": attemptID(check, window)}).Decode(&previous)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return true, nil
	}
	return err == nil && previous.Count < MaxCodeAttempts, err
}

// Consume uses up the attempts of both windows a code accepted now could come
// from, so the code cannot be replayed in this or the next window.
func (p *CodeAttempts) Consume(ctx context.Context, check CodeCheck) error {
	current := helpers.ResetCodeWindow(check.ValidityMinutes)
	for _, window := range []time.Time{current, current.Add(-codeValidity(check))} {
		if _, err := p.raise(ctx, check, window, bson.M{"$max": bson.M{"count": MaxCodeAttempts}}); err != nil {
			return err
		}
	}
	return nil
}

// raise applies update to the window's document, creating it if needed, and
// returns the count it now holds.
func (p *CodeAttempts) raise(ctx context.Context, check CodeCheck, window time.Time, update bson.M) (int, error) {
	// The TTL index removes the document well after the code it counts expired.
	update["$setOnInsert"] = bson.M{"expiresAt": window.Add(3 * codeValidity(check))}
	var attempt entities.AuthCodeAttempt
	err := p.attemptRepository.Collection().FindOneAndUpdate(ctx, bson.M{"_id": attemptID(check, window)}, update,
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&attempt)
	return attempt.Count, err
}

func attemptID(check CodeCheck, window time.Time) string {
	return check.Purpose + "|" + helpers.NormalizeEmail(check.Email) + "|" + strconv.FormatInt(window.Unix(), 10)
}

func codeValidity(check CodeCheck) time.Duration {
	return time.Duration(check.ValidityMinutes) * time.Minute
}
