package houseTanks

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
)

// The map catalog is contract data shared with the clients, so it is read
// from the fixture both sides use instead of being written out in code.
//
//go:embed fixtures/houseTanksMaps.json
var mapCatalogJSON []byte

var ErrInvalidMap = errors.New("house tanks map is invalid")

type ObstacleStyle string

const (
	StyleDivider    ObstacleStyle = "divider"
	StyleSofa       ObstacleStyle = "sofa"
	StyleRoundTable ObstacleStyle = "roundTable"
	StyleCrate      ObstacleStyle = "crate"
	StyleBookshelf  ObstacleStyle = "bookshelf"
	StylePillar     ObstacleStyle = "pillar"
	StylePlanter    ObstacleStyle = "planter"
)

func (style ObstacleStyle) round() bool {
	return style == StyleRoundTable || style == StylePillar || style == StylePlanter
}

func (style ObstacleStyle) valid() bool {
	switch style {
	case StyleDivider, StyleSofa, StyleRoundTable, StyleCrate, StyleBookshelf, StylePillar, StylePlanter:
		return true
	}
	return false
}

// MapPoint is a position normalized to the arena (0…1, y up).
type MapPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func (point MapPoint) arena() Vector { return Vector{point.X * ArenaWidth, point.Y * ArenaHeight} }

func (point MapPoint) normalized() bool {
	return point.X >= 0 && point.X <= 1 && point.Y >= 0 && point.Y <= 1
}

// MapObstacle is authored normalized; Tone is for renderers only.
type MapObstacle struct {
	X        float64       `json:"x"`
	Y        float64       `json:"y"`
	Width    float64       `json:"width"`
	Height   float64       `json:"height"`
	Rotation float64       `json:"rotation"`
	Style    ObstacleStyle `json:"style"`
	Tone     string        `json:"tone"`
}

func (obstacle MapObstacle) center() Vector { return MapPoint{obstacle.X, obstacle.Y}.arena() }

// size is the obstacle's arena size on a map of the given scale. Round
// shapes and crates are as wide as they are tall.
func (obstacle MapObstacle) size(mapScale float64) (width, height float64) {
	scale := ObstacleBaseScale * GameplayScale * mapScale
	height = ArenaHeight * obstacle.Height * scale
	if obstacle.Style.round() || obstacle.Style == StyleCrate {
		return height, height
	}
	return ArenaWidth * obstacle.Width * scale, height
}

// clearance is the gap between a point and the obstacle's authored shape.
func (obstacle MapObstacle) clearance(point Vector, mapScale float64) float64 {
	width, height := obstacle.size(mapScale)
	offset := point.sub(obstacle.center())
	if obstacle.Style.round() {
		return math.Max(0, offset.length()-width/2)
	}
	local := offset.rotated(-obstacle.Rotation)
	return math.Hypot(math.Max(0, math.Abs(local.X)-width/2), math.Max(0, math.Abs(local.Y)-height/2))
}

type MapLine struct {
	Start MapPoint `json:"start"`
	End   MapPoint `json:"end"`
}

// MapDefinition is one arena. Scale shrinks tank, shot, joker and furniture
// sizes on crowded-match maps; speeds and timings never change.
type MapDefinition struct {
	ID           MapID         `json:"id"`
	Version      int           `json:"version"`
	Scale        float64       `json:"scale"`
	MinPlayers   int           `json:"minPlayers"`
	MaxPlayers   int           `json:"maxPlayers"`
	SpawnPoints  []MapPoint    `json:"spawnPoints"`
	Obstacles    []MapObstacle `json:"obstacles"`
	FloorLines   []MapLine     `json:"floorLines"`
	FloorMarkers []MapPoint    `json:"floorMarkers"`
}

func (definition MapDefinition) Supports(players int) bool {
	return players >= definition.MinPlayers && players <= definition.MaxPlayers
}

// ValidationIssues ports the Swift catalog checks and adds the seat rules
// online play relies on.
func (definition MapDefinition) ValidationIssues() []string {
	var issues []string
	if definition.Version < 1 {
		issues = append(issues, "map version must be positive")
	}
	if definition.Scale <= 0 || definition.Scale > 1 {
		issues = append(issues, "map scale must be in (0, 1]")
	}
	if definition.MinPlayers < MinimumPlayers || definition.MaxPlayers < definition.MinPlayers || definition.MaxPlayers > MaximumPlayers {
		issues = append(issues, "player range is invalid")
	}
	if len(definition.SpawnPoints) < definition.MaxPlayers {
		issues = append(issues, "every seat needs a spawn point")
	}
	clearance := SpawnClearance * definition.Scale
	for index, spawn := range definition.SpawnPoints {
		if !spawn.normalized() {
			issues = append(issues, fmt.Sprintf("spawn %d is outside normalized bounds", index))
			continue
		}
		point := spawn.arena()
		frame := ArenaFrame().inset(TankRadius*definition.Scale, TankRadius*definition.Scale)
		if !frame.contains(point) {
			issues = append(issues, fmt.Sprintf("spawn %d touches the wall", index))
		}
		for obstacleIndex, obstacle := range definition.Obstacles {
			if obstacle.clearance(point, definition.Scale) < clearance {
				issues = append(issues, fmt.Sprintf("spawn %d overlaps obstacle %d clearance", index, obstacleIndex))
			}
		}
		for other := 0; other < index; other++ {
			if definition.SpawnPoints[other].normalized() && point.distance(definition.SpawnPoints[other].arena()) < 2*clearance {
				issues = append(issues, fmt.Sprintf("spawn %d is too close to spawn %d", index, other))
			}
		}
	}
	for index, obstacle := range definition.Obstacles {
		width, height := obstacle.size(definition.Scale)
		if obstacle.Width <= 0 || obstacle.Height <= 0 || !obstacle.Style.valid() {
			issues = append(issues, fmt.Sprintf("obstacle %d must have a positive size and a known style", index))
		}
		if !ArenaRect().containsRect(rotatedBounds(obstacle.center(), width, height, obstacle.Rotation)) {
			issues = append(issues, fmt.Sprintf("obstacle %d is outside normalized bounds", index))
		}
	}
	for index, line := range definition.FloorLines {
		if !line.Start.normalized() || !line.End.normalized() {
			issues = append(issues, fmt.Sprintf("floor line %d is outside normalized bounds", index))
		}
	}
	for index, marker := range definition.FloorMarkers {
		if !marker.normalized() {
			issues = append(issues, fmt.Sprintf("floor marker %d is outside normalized bounds", index))
		}
	}
	return issues
}

// rotatedBounds is the axis-aligned box around a rotated rectangle.
func rotatedBounds(center Vector, width, height, rotation float64) Rect {
	boundsWidth := math.Abs(math.Cos(rotation))*width + math.Abs(math.Sin(rotation))*height
	boundsHeight := math.Abs(math.Sin(rotation))*width + math.Abs(math.Cos(rotation))*height
	return Rect{center.X - boundsWidth/2, center.Y - boundsHeight/2, boundsWidth, boundsHeight}
}

// MapCatalog is the contents of houseTanksMaps.json.
type MapCatalog struct {
	CatalogVersion   int             `json:"catalogVersion"`
	ContractRevision string          `json:"contractRevision"`
	ArenaWidth       float64         `json:"arenaWidth"`
	ArenaHeight      float64         `json:"arenaHeight"`
	Maps             []MapDefinition `json:"maps"`
}

var loadCatalog = sync.OnceValues(func() (MapCatalog, error) {
	var catalog MapCatalog
	if err := json.Unmarshal(mapCatalogJSON, &catalog); err != nil {
		return MapCatalog{}, errors.Join(ErrInvalidMap, err)
	}
	if catalog.CatalogVersion != MapCatalogVersion || catalog.ContractRevision != ContractRevision ||
		catalog.ArenaWidth != ArenaWidth || catalog.ArenaHeight != ArenaHeight || len(catalog.Maps) == 0 {
		return MapCatalog{}, ErrInvalidMap
	}
	covered := make([]bool, MaximumPlayers+1)
	for _, definition := range catalog.Maps {
		if issues := definition.ValidationIssues(); len(issues) > 0 {
			return MapCatalog{}, fmt.Errorf("%w: %s: %v", ErrInvalidMap, definition.ID, issues)
		}
		for players := definition.MinPlayers; players <= definition.MaxPlayers; players++ {
			covered[players] = true
		}
	}
	for players := MinimumPlayers; players <= MaximumPlayers; players++ {
		if !covered[players] {
			return MapCatalog{}, fmt.Errorf("%w: no map for %d players", ErrInvalidMap, players)
		}
	}
	return catalog, nil
})

// Maps returns the catalog in its authored order. The embedded catalog is
// validated by tests, so an error here is a build defect.
func Maps() []MapDefinition {
	catalog, err := loadCatalog()
	if err != nil {
		panic(err)
	}
	return append([]MapDefinition(nil), catalog.Maps...)
}

func MapByID(id MapID) (MapDefinition, bool) {
	for _, definition := range Maps() {
		if definition.ID == id {
			return definition, true
		}
	}
	return MapDefinition{}, false
}

// SelectMap draws the match's map among those that seat the roster, in
// catalog order, from the match's generator.
func SelectMap(random *Random, players int) (MapDefinition, error) {
	var candidates []MapDefinition
	for _, definition := range Maps() {
		if definition.Supports(players) {
			candidates = append(candidates, definition)
		}
	}
	if len(candidates) == 0 {
		return MapDefinition{}, ErrInvalidMap
	}
	return candidates[random.Below(len(candidates))], nil
}

// ValidateMapCatalog reports why the embedded catalog cannot be used.
func ValidateMapCatalog() error {
	_, err := loadCatalog()
	return err
}
