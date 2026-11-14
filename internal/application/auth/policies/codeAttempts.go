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

// CodeResult is the outcome of one attempt at an emailed code.
type CodeResult struct {
	Valid bool
	// TooManyAttempts reports that the window's attempts ran out. The code
	// was not compared, so the result says nothing about it.
	TooManyAttempts bool
	// Consumed reports a refusal in a window whose code was already used
	// successfully, so the request may repeat one that went through. The code
	// itself is not confirmed; the caller must compare it.
	Consumed bool
}

// Refusal is the error for a refused code; invalidKey is the flow's own
// "invalid or expired" message.
func (r CodeResult) Refusal(invalidKey string) error {
	if r.TooManyAttempts {
		return helpers.NewLocalizedError("auth.error.too_many_code_attempts")
	}
	return helpers.NewLocalizedError(invalidKey)
}

// Verify checks check.Code. Every attempt, right or wrong, is counted first
// in one atomic update, so parallel requests cannot get more than
// MaxCodeAttempts codes compared per email and window.
func (p *CodeAttempts) Verify(ctx context.Context, check CodeCheck) (CodeResult, error) {
	current := helpers.ResetCodeWindow(check.ValidityMinutes)
	attempt, err := p.raise(ctx, check, current, bson.M{"$inc": bson.M{"count": 1}})
	if err != nil {
		return CodeResult{}, err
	}
	if attempt.Count > MaxCodeAttempts {
		// Repeats after a successful use are let through only a few times,
		// so they cannot become an unlimited oracle.
		repeat := attempt.Consumed && attempt.Count <= 2*MaxCodeAttempts
		return CodeResult{TooManyAttempts: true, Consumed: repeat}, nil
	}

	window, matched := helpers.MatchResetCode(check.Subject, check.Code, check.Secret, current, check.ValidityMinutes)
	if !matched || window.Equal(current) {
		return CodeResult{Valid: matched}, nil
	}
	// A code from the previous window stays refused once that window was
	// used up or the code was consumed. The code has expired by then, so
	// this is an ordinary invalid code, not too many attempts.
	var previous entities.AuthCodeAttempt
	err = p.attemptRepository.Collection().FindOne(ctx, bson.M{"_id": attemptID(check, window)}).Decode(&previous)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return CodeResult{Valid: true}, nil
	}
	if err != nil {
		return CodeResult{}, err
	}
	return CodeResult{Valid: previous.Count < MaxCodeAttempts, Consumed: previous.Consumed}, nil
}

// Consume uses up the attempts of both windows a code accepted now could come
// from, so the code cannot be replayed in this or the next window.
func (p *CodeAttempts) Consume(ctx context.Context, check CodeCheck) error {
	current := helpers.ResetCodeWindow(check.ValidityMinutes)
	for _, window := range []time.Time{current, current.Add(-codeValidity(check))} {
		update := bson.M{"$max": bson.M{"count": MaxCodeAttempts}, "$set": bson.M{"consumed": true}}
		if _, err := p.raise(ctx, check, window, update); err != nil {
			return err
		}
	}
	return nil
}

// raise applies update to the window's document, creating it if needed, and
// returns the document as it now is.
func (p *CodeAttempts) raise(ctx context.Context, check CodeCheck, window time.Time, update bson.M) (entities.AuthCodeAttempt, error) {
	// The TTL index removes the document well after the code it counts expired.
	update["$setOnInsert"] = bson.M{"expiresAt": window.Add(3 * codeValidity(check))}
	var attempt entities.AuthCodeAttempt
	err := p.attemptRepository.Collection().FindOneAndUpdate(ctx, bson.M{"_id": attemptID(check, window)}, update,
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&attempt)
	return attempt, err
}

func attemptID(check CodeCheck, window time.Time) string {
	return check.Purpose + "|" + helpers.NormalizeEmail(check.Email) + "|" + strconv.FormatInt(window.Unix(), 10)
}

func codeValidity(check CodeCheck) time.Duration {
	return time.Duration(check.ValidityMinutes) * time.Minute
}
