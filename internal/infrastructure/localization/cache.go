package localization

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"houseflowApi/internal/abstract"
	localizationAbstract "houseflowApi/internal/application/localization/abstract"
	"houseflowApi/internal/data/entities"
	"houseflowApi/internal/helpers"

	"go.mongodb.org/mongo-driver/bson"
)

type Cache struct {
	localizationRepository *abstract.DbRepository[entities.Localization]
	mu                     sync.RWMutex
	messages               map[string]map[string]string
	plaintexts             map[string]map[string]string
}

func NewCache(localizationRepository *abstract.DbRepository[entities.Localization]) *Cache {
	return &Cache{
		localizationRepository: localizationRepository,
		messages:               make(map[string]map[string]string),
		plaintexts:             make(map[string]map[string]string),
	}
}

func (c *Cache) Load(ctx context.Context) error {
	messageItems, err := c.localizationRepository.FindManyByFilter(ctx, bson.M{
		"type": entities.Message, "language": entities.English,
	})
	if err != nil {
		return err
	}
	plaintextItems, err := c.localizationRepository.FindManyByFilter(ctx, bson.M{
		"type": entities.Plaintext, "language": entities.English,
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

	c.mu.Lock()
	c.messages = messages
	c.plaintexts = plaintexts
	c.mu.Unlock()
	return nil
}

func (c *Cache) GetPlaintexts(language string) (map[string]string, bool) {
	c.mu.RLock()
	values, ok := c.plaintexts[language]
	if !ok {
		c.mu.RUnlock()
		return nil, false
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	c.mu.RUnlock()
	return result, true
}

func (c *Cache) MergePlaintexts(language string, values map[string]string) {
	c.mu.Lock()
	for key, value := range values {
		setLocalizedValue(c.plaintexts, language, key, value)
	}
	if _, ok := c.plaintexts[language]; !ok {
		c.plaintexts[language] = make(map[string]string)
	}
	c.mu.Unlock()
}

func (c *Cache) SetLocalization(localization entities.Localization) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch localization.Type {
	case entities.Message:
		setLocalizedValue(c.messages, string(localization.Language), localization.Key, localization.Value)
	case entities.Plaintext:
		setLocalizedValue(c.plaintexts, string(localization.Language), localization.Key, localization.Value)
	}
}

func (c *Cache) LocalizeMessage(ctx context.Context, language string, keyOrMessage string) string {
	normalizedLanguage := helpers.NormalizeLanguage(language)
	if strings.Contains(keyOrMessage, "; ") {
		parts := strings.Split(keyOrMessage, "; ")
		for index, part := range parts {
			parts[index] = c.LocalizeMessage(ctx, normalizedLanguage, part)
		}
		return strings.Join(parts, "; ")
	}

	key, args := helpers.SplitLocalizationMessage(keyOrMessage)
	value, ok := c.cachedMessage(normalizedLanguage, key)
	if !ok && normalizedLanguage != helpers.DefaultLanguage {
		value, ok = c.findMessage(ctx, normalizedLanguage, key)
	}
	if !ok {
		value, ok = c.cachedMessage(helpers.DefaultLanguage, key)
	}
	if !ok || value == "" {
		return missingMessageFallback(key, args)
	}
	if len(args) == 0 {
		return value
	}
	return formatLocalizedMessage(value, args)
}

func (c *Cache) findMessage(ctx context.Context, language string, key string) (string, bool) {
	items, err := c.localizationRepository.FindManyByFilter(ctx, bson.M{
		"type": entities.Message, "language": entities.LocalizationLanguage(language), "key": key,
	})
	if err != nil || len(items) == 0 {
		return "", false
	}
	c.SetLocalization(items[0])
	return items[0].Value, true
}

func (c *Cache) cachedMessage(language string, key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	values, ok := c.messages[language]
	if !ok {
		return "", false
	}
	value, ok := values[key]
	return value, ok
}

func setLocalizedValue(target map[string]map[string]string, language string, key string, value string) {
	if _, ok := target[language]; !ok {
		target[language] = make(map[string]string)
	}
	target[language][key] = value
}

func formatLocalizedMessage(value string, args []string) string {
	if strings.Contains(value, "%s") {
		values := make([]any, 0, len(args))
		for _, arg := range args {
			values = append(values, arg)
		}
		return fmt.Sprintf(value, values...)
	}
	for index, arg := range args {
		value = strings.ReplaceAll(value, fmt.Sprintf("{%d}", index), arg)
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

var _ localizationAbstract.Cache = (*Cache)(nil)
var _ helpers.MessageLocalizer = (*Cache)(nil)
