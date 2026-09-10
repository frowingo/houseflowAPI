package tests

import (
	"testing"

	imageAssetCommands "houseflowApi/internal/application/imageAsset/commands"
	imageAssetQueries "houseflowApi/internal/application/imageAsset/queries"
	userCommands "houseflowApi/internal/application/user/commands"
	userQueries "houseflowApi/internal/application/user/queries"
	"houseflowApi/internal/infrastructure/cqrs"
	"houseflowApi/internal/models/dtos"
)

func TestUserHandlersCreateQueryListAndDeleteUser(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	email := "phase-three@example.test"

	created, err := cqrs.Send[*dtos.NewUserModel](fixture.ctx, fixture.sender, userCommands.CreateUserCommand{
		Firstname: "Phase", Lastname: "Three", Email: email, Password: "secure-password", Language: "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Email != email {
		t.Fatalf("created email = %q, want %q", created.Email, email)
	}

	queried, err := cqrs.Send[*dtos.UserResultModel](fixture.ctx, fixture.sender, userQueries.GetUserByEmailQuery{Email: email})
	if err != nil {
		t.Fatal(err)
	}
	if queried.Firstname != "Phase" || queried.Lastname != "Three" {
		t.Fatalf("unexpected queried user: %+v", queried)
	}

	users, err := cqrs.Send[[]dtos.UserResultModel](fixture.ctx, fixture.sender, userQueries.ListUsersQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("user count = %d, want 1", len(users))
	}

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, userCommands.DeleteUserCommand{UserID: queried.Id}); err != nil {
		t.Fatal(err)
	}
	if _, err := cqrs.Send[*dtos.UserResultModel](fixture.ctx, fixture.sender, userQueries.GetUserByEmailQuery{Email: email}); err == nil {
		t.Fatal("deleted user is still queryable")
	}
}

func TestImageAssetUpdateInvalidatesCategoryCache(t *testing.T) {
	fixture := newConcurrencyFixture(t)
	category := "avatar"
	publicID := "avatar_phase-three.png"

	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, imageAssetCommands.CreateImageAssetCommand{
		Category: category, FileName: "phase-three.png", FileURL: "https://example.test/one.png", IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}

	images, err := cqrs.Send[[]dtos.ImageAssetResultModel](fixture.ctx, fixture.sender, imageAssetQueries.GetImagesByCategoryQuery{
		Category: category,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].PublicID != publicID {
		t.Fatalf("unexpected cached image list: %+v", images)
	}

	isActive := false
	updatedURL := "https://example.test/two.png"
	if _, err := cqrs.Send[cqrs.NoResult](fixture.ctx, fixture.sender, imageAssetCommands.UpdateImageAssetCommand{
		PublicID: publicID, FileURL: &updatedURL, IsActive: &isActive,
	}); err != nil {
		t.Fatal(err)
	}

	images, err = cqrs.Send[[]dtos.ImageAssetResultModel](fixture.ctx, fixture.sender, imageAssetQueries.GetImagesByCategoryQuery{
		Category: category,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("inactive image remained in category cache: %+v", images)
	}
	if _, err := cqrs.Send[*dtos.ImageAssetResultModel](fixture.ctx, fixture.sender, imageAssetQueries.GetImageByPublicIDQuery{
		PublicID: publicID,
	}); err == nil {
		t.Fatal("inactive image is still publicly queryable")
	}
}
