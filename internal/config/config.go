package config

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
)

const defaultHouseJoinCodeValidityMinutes = 2

//go:embed staticFiles/code-email.html
var staticFiles embed.FS

func ReadStaticFile(name string) ([]byte, error) {
	return staticFiles.ReadFile("staticFiles/" + name)
}

type ConfigModel struct {
	External ConfigExternal `json:"external"`
	Internal ConfigInternal `json:"internal"`
}

type ConfigExternal struct {
	Mongo      ConfigMongo      `json:"mongo"`
	Cloudinary ConfigCloudinary `json:"cloudinary"`
	Redis      ConfigRedis      `json:"redis"`
	SocialAuth ConfigSocialAuth `json:"socialAuth"`
}

type ConfigInternal struct {
	JWT           ConfigJWT           `json:"jwt"`
	PasswordReset ConfigPasswordReset `json:"passwordReset"`
	HouseJoinCode ConfigHouseJoinCode `json:"houseJoinCode"`
	SMTP          ConfigSMTP          `json:"smtp"`
}

type ConfigPasswordReset struct {
	Secret          string `json:"secret"`
	ValidityMinutes int    `json:"validityMinutes"`
}

type ConfigHouseJoinCode struct {
	Secret          string `json:"secret"`
	ValidityMinutes int    `json:"validityMinutes"`
}

type ConfigJWT struct {
	ApiSecret string `json:"apiSecret"`
}

type ConfigSMTP struct {
	Password string `json:"password"`
}

type ConfigMongo struct {
	ConnectionString string `json:"connectionString"`
	DbName           string `json:"dbName"`
}

type ConfigRedis struct {
	URL string `json:"url"`
}

type ConfigCloudinary struct {
	CloudName string `json:"cloudName"`
	APIKey    string `json:"apiKey"`
	APISecret string `json:"apiSecret"`
	Folder    string `json:"folder"`
}

// ConfigSocialAuth holds the third-party sign-in providers. A provider whose
// values are missing is disabled; a partially configured provider is a
// startup error so it is never switched off by accident.
type ConfigSocialAuth struct {
	Google   ConfigGoogleAuth   `json:"google"`
	Apple    ConfigAppleAuth    `json:"apple"`
	Facebook ConfigFacebookAuth `json:"facebook"`
	Snapchat ConfigSnapchatAuth `json:"snapchat"`
	// TokenEncryptionKey is a base64 encoded 32 byte key used to encrypt the
	// provider tokens that must be kept (the Apple refresh token).
	TokenEncryptionKey string `json:"tokenEncryptionKey"`
}

type ConfigGoogleAuth struct {
	ClientIDs []string `json:"clientIds"`
}

func (c ConfigGoogleAuth) Enabled() bool { return len(c.ClientIDs) > 0 }

type ConfigAppleAuth struct {
	ClientIDs  []string `json:"clientIds"`
	TeamID     string   `json:"teamId"`
	KeyID      string   `json:"keyId"`
	PrivateKey string   `json:"privateKey"`
}

func (c ConfigAppleAuth) Enabled() bool { return len(c.ClientIDs) > 0 }

func (c ConfigAppleAuth) configured() bool {
	return len(c.ClientIDs) > 0 || c.TeamID != "" || c.KeyID != "" || c.PrivateKey != ""
}

type ConfigFacebookAuth struct {
	AppID     string `json:"appId"`
	AppSecret string `json:"appSecret"`
	// GraphVersion pins the Graph API version, e.g. "v23.0"; empty uses the
	// adapter's default.
	GraphVersion string `json:"graphVersion"`
}

func (c ConfigFacebookAuth) Enabled() bool { return c.AppID != "" }

type ConfigSnapchatAuth struct {
	ClientID string `json:"clientId"`
}

func (c ConfigSnapchatAuth) Enabled() bool { return c.ClientID != "" }

func isDebugMode() bool {
	return os.Getenv("APP_ENV") != "production"
}

func LoadConfig() (*ConfigModel, error) {
	configFilePath := "./internal/config/config.json"

	file, err := os.ReadFile(configFilePath)
	var config ConfigModel
	if err != nil && isDebugMode() {
		return nil, errors.New("failed to read config file:" + err.Error())
	}

	if err == nil {
		if err := json.Unmarshal(file, &config); err != nil {
			return nil, errors.New("config.json cannot deserialize:" + err.Error())
		}
	}

	applyEnvOverrides(&config)

	return &config, nil
}

func applyEnvOverrides(config *ConfigModel) {
	if config.Internal.HouseJoinCode.ValidityMinutes <= 0 {
		config.Internal.HouseJoinCode.ValidityMinutes = defaultHouseJoinCodeValidityMinutes
	}

	if uri := os.Getenv("MONGO_URI"); uri != "" {
		config.External.Mongo.ConnectionString = uri
	}

	if db := os.Getenv("MONGO_DB"); db != "" {
		config.External.Mongo.DbName = db
	}

	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		config.External.Redis.URL = redisURL
	}

	if cloudName := os.Getenv("CLOUDINARY_CLOUD_NAME"); cloudName != "" {
		config.External.Cloudinary.CloudName = cloudName
	}

	if apiKey := os.Getenv("CLOUDINARY_API_KEY"); apiKey != "" {
		config.External.Cloudinary.APIKey = apiKey
	}

	if apiSecret := os.Getenv("CLOUDINARY_API_SECRET"); apiSecret != "" {
		config.External.Cloudinary.APISecret = apiSecret
	}

	if folder := os.Getenv("CLOUDINARY_FOLDER"); folder != "" {
		config.External.Cloudinary.Folder = folder
	}

	if jwtSecret := os.Getenv("JWT_SECRET"); jwtSecret != "" {
		config.Internal.JWT.ApiSecret = jwtSecret
	}

	if resetSecret := os.Getenv("RESET_CODE_SECRET"); resetSecret != "" {
		config.Internal.PasswordReset.Secret = resetSecret
	}

	if validity := os.Getenv("RESET_CODE_VALIDITY_MINUTES"); validity != "" {
		minutes, err := strconv.Atoi(validity)
		if err == nil && minutes > 0 {
			config.Internal.PasswordReset.ValidityMinutes = minutes
		}
	}

	if joinCodeSecret := os.Getenv("JOIN_CODE_SECRET"); joinCodeSecret != "" {
		config.Internal.HouseJoinCode.Secret = joinCodeSecret
	}

	if smtpPassword := os.Getenv("SMTP_PASSWORD"); smtpPassword != "" {
		config.Internal.SMTP.Password = smtpPassword
	}

	applySocialAuthEnvOverrides(&config.External.SocialAuth)
}

func applySocialAuthEnvOverrides(config *ConfigSocialAuth) {
	if clientIDs := splitList(os.Getenv("GOOGLE_CLIENT_IDS")); len(clientIDs) > 0 {
		config.Google.ClientIDs = clientIDs
	}
	if clientIDs := splitList(os.Getenv("APPLE_CLIENT_IDS")); len(clientIDs) > 0 {
		config.Apple.ClientIDs = clientIDs
	}
	if teamID := os.Getenv("APPLE_TEAM_ID"); teamID != "" {
		config.Apple.TeamID = teamID
	}
	if keyID := os.Getenv("APPLE_KEY_ID"); keyID != "" {
		config.Apple.KeyID = keyID
	}
	if privateKey := os.Getenv("APPLE_PRIVATE_KEY"); privateKey != "" {
		// Secrets stored on one line carry the PEM line breaks as "\n".
		config.Apple.PrivateKey = strings.ReplaceAll(privateKey, `\n`, "\n")
	}
	if appID := os.Getenv("FACEBOOK_APP_ID"); appID != "" {
		config.Facebook.AppID = appID
	}
	if appSecret := os.Getenv("FACEBOOK_APP_SECRET"); appSecret != "" {
		config.Facebook.AppSecret = appSecret
	}
	if graphVersion := os.Getenv("FACEBOOK_GRAPH_VERSION"); graphVersion != "" {
		config.Facebook.GraphVersion = graphVersion
	}
	if clientID := os.Getenv("SNAPCHAT_CLIENT_ID"); clientID != "" {
		config.Snapchat.ClientID = clientID
	}
	if key := os.Getenv("SOCIAL_TOKEN_ENC_KEY"); key != "" {
		config.TokenEncryptionKey = key
	}
}

func splitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func Validate(config *ConfigModel) error {
	if err := ValidateMongo(config.External.Mongo); err != nil {
		return err
	}
	if config.Internal.JWT.ApiSecret == "" {
		return errors.New("jwt secret is required")
	}
	if config.Internal.PasswordReset.Secret == "" {
		return errors.New("password reset secret is required")
	}
	if config.Internal.PasswordReset.ValidityMinutes <= 0 {
		return errors.New("password reset validity must be greater than zero")
	}
	if config.Internal.HouseJoinCode.Secret == "" {
		return errors.New("house join code secret is required")
	}
	if config.Internal.HouseJoinCode.ValidityMinutes <= 0 {
		return errors.New("house join code validity must be greater than zero")
	}
	if config.Internal.SMTP.Password == "" {
		return errors.New("smtp password is required")
	}
	return ValidateSocialAuth(config.External.SocialAuth)
}

// ValidateSocialAuth rejects providers that are only partly configured.
func ValidateSocialAuth(config ConfigSocialAuth) error {
	if config.Apple.configured() {
		if len(config.Apple.ClientIDs) == 0 || config.Apple.TeamID == "" || config.Apple.KeyID == "" || config.Apple.PrivateKey == "" {
			return errors.New("apple sign-in needs APPLE_CLIENT_IDS, APPLE_TEAM_ID, APPLE_KEY_ID and APPLE_PRIVATE_KEY")
		}
		key, err := base64.StdEncoding.DecodeString(config.TokenEncryptionKey)
		if err != nil || len(key) != 32 {
			return errors.New("apple sign-in needs SOCIAL_TOKEN_ENC_KEY as a base64 encoded 32 byte key")
		}
	}
	if (config.Facebook.AppID == "") != (config.Facebook.AppSecret == "") {
		return errors.New("facebook sign-in needs both FACEBOOK_APP_ID and FACEBOOK_APP_SECRET")
	}
	return nil
}

func ValidateMongo(config ConfigMongo) error {
	if config.ConnectionString == "" {
		return errors.New("mongo connection string is required")
	}
	if config.DbName == "" {
		return errors.New("mongo db name is required")
	}
	return nil
}

func MustLoadConfig() (*ConfigModel, error) {
	config, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if err := Validate(config); err != nil {
		return nil, err
	}
	return config, nil
}
