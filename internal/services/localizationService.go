package services

import (
	"fmt"
	"houseflowApi/internal/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"
	"houseflowApi/internal/models/dtos"
	"sort"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type LocalizationService struct {
	dbRepository       *abstract.DbRepository[entities.Localization]
	languageRepository *abstract.DbRepository[entities.LocalizationLanguageOption]
	mu                 sync.RWMutex
	messages           map[string]map[string]string
	plaintexts         map[string]map[string]string
}

func NewLocalizationService(
	dbRepository *abstract.DbRepository[entities.Localization],
	languageRepository *abstract.DbRepository[entities.LocalizationLanguageOption],
) *LocalizationService {
	return &LocalizationService{
		dbRepository:       dbRepository,
		languageRepository: languageRepository,
		messages:           make(map[string]map[string]string),
		plaintexts:         make(map[string]map[string]string),
	}
}

func (s *LocalizationService) LoadCache() error {
	messageItems, err := s.dbRepository.FindManyByFilter(bson.M{
		"type":     entities.Message,
		"language": entities.English,
	})
	if err != nil {
		return err
	}

	plaintextItems, err := s.dbRepository.FindManyByFilter(bson.M{
		"type":     entities.Plaintext,
		"language": entities.English,
	})
	if err != nil {
		return err
	}

	messages := make(map[string]map[string]string)
	plaintexts := make(map[string]map[string]string)

	for _, item := range messageItems {
		setLocalizedValue(messages, string(item.Language), item.Key, item.Value)
	}
	for _, item := range plaintextItems {
		setLocalizedValue(plaintexts, string(item.Language), item.Key, item.Value)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = messages
	s.plaintexts = plaintexts

	return nil
}

func (s *LocalizationService) InsertLocalizations(models []dtos.LocalizationRequestModel) error {
	if len(models) == 0 {
		return helpers.NewLocalizedError("common.error.request_body_required")
	}

	for _, model := range models {
		if !helpers.IsSupportedLanguage(model.Language) {
			return helpers.NewLocalizedError("localization.error.unsupported_language")
		}
		if !helpers.IsSupportedLocalizationType(model.Type) {
			return helpers.NewLocalizedError("localization.error.unsupported_type")
		}

		entity := model.ToEntity()
		entity.Language = entities.LocalizationLanguage(helpers.NormalizeLanguage(string(entity.Language)))
		entity.Type = entities.LocalizationType(helpers.NormalizeLocalizationType(string(entity.Type)))
		if _, err := s.dbRepository.Insert(entity); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return helpers.NewLocalizedError("localization.error.duplicate_key")
			}
			return err
		}

		s.setCache(entity)
	}

	return nil
}

func (s *LocalizationService) GetPlaintexts(language string) ([]dtos.LocalizationPlaintextResponseModel, error) {
	normalizedLanguage := helpers.NormalizeLanguage(language)
	if !helpers.IsSupportedLanguage(normalizedLanguage) {
		return nil, helpers.NewLocalizedError("localization.error.unsupported_language")
	}

	s.mu.RLock()
	cached := s.plaintexts[normalizedLanguage]
	if cached != nil {
		defer s.mu.RUnlock()
		return plaintextResponseModels(normalizedLanguage, cached), nil
	}
	s.mu.RUnlock()

	items, err := s.dbRepository.FindManyByFilter(bson.M{
		"type":     entities.Plaintext,
		"language": normalizedLanguage,
	})
	if err != nil {
		return nil, err
	}

	plaintexts := make(map[string]string, len(items))
	for _, item := range items {
		plaintexts[item.Key] = item.Value
	}

	s.mu.Lock()
	s.plaintexts[normalizedLanguage] = plaintexts
	s.mu.Unlock()

	return plaintextResponseModels(normalizedLanguage, plaintexts), nil
}

func (s *LocalizationService) GetLanguages() ([]dtos.LocalizationLanguageResponseModel, error) {
	languages, err := s.languageRepository.FindAll()
	if err != nil {
		return nil, err
	}

	sortLanguages(languages)
	return languageResponseModels(languages), nil
}

func (s *LocalizationService) GetLanguage(prefix string) ([]dtos.LocalizationLanguageResponseModel, error) {
	normalizedPrefix := normalizeLanguageOptionPrefix(prefix)
	languages, err := s.languageRepository.FindManyByFilter(bson.M{
		"code":     normalizedPrefix,
		"isActive": true,
	})
	if err != nil {
		return nil, err
	}

	sortLanguages(languages)
	return languageResponseModels(languages), nil
}

func (s *LocalizationService) InsertLocalizationLanguage(model dtos.LocalizationLanguageRequestModel) error {
	normalizedPrefix := normalizeLanguageOptionPrefix(model.Prefix)
	if normalizedPrefix == "" {
		return helpers.NewLocalizedError("localization.error.unsupported_language")
	}

	entity := model.ToEntity()
	entity.Code = normalizedPrefix
	if _, err := s.languageRepository.Insert(entity); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return helpers.NewLocalizedError("localization.error.duplicate_key")
		}
		return err
	}

	return nil
}

func (s *LocalizationService) LocalizeMessage(language string, keyOrMessage string) string {
	normalizedLanguage := helpers.NormalizeLanguage(language)
	if strings.Contains(keyOrMessage, "; ") {
		parts := strings.Split(keyOrMessage, "; ")
		for i, part := range parts {
			parts[i] = s.LocalizeMessage(normalizedLanguage, part)
		}
		return strings.Join(parts, "; ")
	}

	key, args := helpers.SplitLocalizationMessage(keyOrMessage)
	value, ok := s.cachedMessage(normalizedLanguage, key)
	if !ok && normalizedLanguage != helpers.DefaultLanguage {
		value, ok = s.findMessage(normalizedLanguage, key)
	}
	if !ok {
		value, ok = s.cachedMessage(helpers.DefaultLanguage, key)
	}
	if !ok || value == "" {
		return missingMessageFallback(key, args)
	}

	if len(args) == 0 {
		return value
	}
	return formatLocalizedMessage(value, args)
}

func (s *LocalizationService) findMessage(language string, key string) (string, bool) {
	items, err := s.dbRepository.FindManyByFilter(bson.M{
		"type":     entities.Message,
		"language": entities.LocalizationLanguage(language),
		"key":      key,
	})
	if err != nil || len(items) == 0 {
		return "", false
	}

	return items[0].Value, true
}

func (s *LocalizationService) cachedMessage(language string, key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	values, ok := s.messages[language]
	if !ok {
		return "", false
	}

	value, ok := values[key]
	return value, ok
}

func (s *LocalizationService) setCache(entity entities.Localization) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch entity.Type {
	case entities.Message:
		setLocalizedValue(s.messages, string(entity.Language), entity.Key, entity.Value)
	case entities.Plaintext:
		setLocalizedValue(s.plaintexts, string(entity.Language), entity.Key, entity.Value)
	}
}

func setLocalizedValue(target map[string]map[string]string, language string, key string, value string) {
	if _, ok := target[language]; !ok {
		target[language] = make(map[string]string)
	}
	target[language][key] = value
}

func plaintextResponseModels(language string, plaintexts map[string]string) []dtos.LocalizationPlaintextResponseModel {
	keys := make([]string, 0, len(plaintexts))
	for key := range plaintexts {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]dtos.LocalizationPlaintextResponseModel, 0, len(keys))
	for _, key := range keys {
		result = append(result, dtos.LocalizationPlaintextResponseModel{
			Key:      key,
			Language: language,
			Value:    plaintexts[key],
		})
	}

	return result
}

func sortLanguages(languages []entities.LocalizationLanguageOption) {
	sort.Slice(languages, func(i, j int) bool {
		return languages[i].Code < languages[j].Code
	})
}

func languageResponseModels(languages []entities.LocalizationLanguageOption) []dtos.LocalizationLanguageResponseModel {
	result := make([]dtos.LocalizationLanguageResponseModel, 0, len(languages))
	for _, language := range languages {
		result = append(result, dtos.LocalizationLanguageResponseModel{
			Prefix:     language.Code,
			Name:       language.Name,
			NativeName: language.NativeName,
			IsDefault:  language.IsDefault,
			IsActive:   language.IsActive,
			Image:      language.Image,
		})
	}
	return result
}

func normalizeLanguageOptionPrefix(prefix string) string {
	return strings.ToLower(strings.TrimSpace(prefix))
}

func formatLocalizedMessage(value string, args []string) string {
	if strings.Contains(value, "%s") {
		values := make([]any, 0, len(args))
		for _, arg := range args {
			values = append(values, arg)
		}
		return fmt.Sprintf(value, values...)
	}
	for i, arg := range args {
		value = strings.ReplaceAll(value, fmt.Sprintf("{%d}", i), arg)
	}
	if strings.Contains(value, "{detail}") {
		return strings.ReplaceAll(value, "{detail}", strings.Join(args, ", "))
	}
	if strings.HasSuffix(value, ":") {
		return value + " " + strings.Join(args, ", ")
	}
	return value + ": " + strings.Join(args, ", ")
}

func missingMessageFallback(key string, args []string) string {
	if len(args) == 0 {
		return key
	}
	return key + ": " + strings.Join(args, ", ")
}
