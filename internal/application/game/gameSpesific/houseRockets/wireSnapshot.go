package houseRockets

// SnapshotForConnection projects only this controller's generation. Internal
// outcome/proposed winner is deliberately hidden until result commit (Paket 5).
func SnapshotForConnection(frame RuntimeFrame, playerID, connectionID string, grant *HouseRocketsControlGrantedModel) HouseRocketsSnapshotModel {
	model := HouseRocketsSnapshotModel{SessionID: frame.World.SessionID, GameKey: GameKey, RuntimeEpoch: frame.RuntimeEpoch, StateSequence: frame.StateSequence, Tick: frame.World.Tick,
		ElapsedSeconds: frame.World.ElapsedSeconds, Phase: frame.Phase, CourseVersion: CourseVersion, CameraX: frame.World.CameraX, CourseAngle: frame.World.CourseAngle,
		Players: make([]HouseRocketsPlayerModel, 0, len(frame.World.Players)), Gates: make([]HouseRocketsGateModel, 0, len(frame.World.Gates)), SpeedFields: make([]HouseRocketsSpeedFieldModel, 0, len(frame.World.SpeedFields))}
	if frame.CommittedResult != nil && frame.Phase == PhaseEnded {
		model.WinnerID = cloneValue(frame.CommittedResult.WinnerID)
	}
	for index, player := range frame.World.Players {
		control := RuntimePlayerControl{}
		if index < len(frame.Controls) && frame.Controls[index].PlayerID == player.PlayerID {
			control = frame.Controls[index]
		}
		item := HouseRocketsPlayerModel{PlayerID: player.PlayerID, DisplayName: player.DisplayName, Color: player.Color, WorldX: player.WorldX, WorldY: player.WorldY, CourseHeading: player.CourseHeading, IsAlive: player.IsAlive,
			Connected: control.Connected, Distance: player.Distance(), SpeedEffect: cloneValue(player.SpeedEffect), EffectRemainingSeconds: player.EffectRemainingSeconds, EliminatedAtTick: cloneValue(player.EliminatedAtTick), EliminationReason: cloneValue(player.EliminationReason), LastProcessedInputSequence: control.LastProcessedInputSequence}
		if player.IsAlive && frame.Phase == PhasePlaying && grant != nil && grant.RuntimeEpoch == frame.RuntimeEpoch && grant.PlayerID == playerID && player.PlayerID == playerID && control.ConnectionID == connectionID && control.Connected {
			item.ControlGeneration = cloneValue(&grant.ControlGeneration)
		}
		model.Players = append(model.Players, item)
	}
	for _, gate := range frame.World.Gates {
		item := HouseRocketsGateModel{ID: gate.ID, WorldX: gate.WorldX, Sections: make([]HouseRocketsPassageSectionModel, 0, len(gate.Sections))}
		for _, section := range gate.Sections {
			item.Sections = append(item.Sections, HouseRocketsPassageSectionModel{OffsetX: section.OffsetX, LowerY: section.LowerY, UpperY: section.UpperY})
		}
		model.Gates = append(model.Gates, item)
	}
	for _, field := range frame.World.SpeedFields {
		model.SpeedFields = append(model.SpeedFields, HouseRocketsSpeedFieldModel{ID: field.ID, WorldX: field.WorldX, Effect: field.Effect, Phase: field.Phase, PeriodSeconds: field.PeriodSeconds})
	}
	return model
}
