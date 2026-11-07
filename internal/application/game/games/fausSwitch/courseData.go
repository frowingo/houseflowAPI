// Code generated from houseflowApp FausCrew/shared/Sources/FausCrewCore/Games/FausSwitch/FausSwitchCourse.swift
// (FausSwitchCourse.standard, courseVersion 7). DO NOT EDIT: regenerate from the Swift source.

package fausSwitch

// standardCourseVersion is FausSwitchCourse.currentVersion of the source.
const standardCourseVersion = 7

// Standard course data in course units, in Swift's order; ids are list positions.
var (
	standardLength   = 20400.0
	turnStartX       = 9100.0
	secondTurnStartX = 15450.0
	turnRadius       = 395.0
)

// standardBlocks are block(centerX, width, minY, maxY) calls.
var standardBlocks = [...][4]float64{
	{1000.0, 62.0, 0.0, 150.0},
	{1080.0, 70.0, 232.0, 287.0},
	{1400.0, 62.0, 240.0, worldHeight},
	{1480.0, 70.0, 103.0, 158.0},
	{1820.0, 58.0, 0.0, 118.0},
	{1870.0, 64.0, 172.0, 218.0},
	{1920.0, 58.0, 272.0, worldHeight},
	{2440.0, 58.0, 272.0, worldHeight},
	{2490.0, 64.0, 172.0, 218.0},
	{2540.0, 58.0, 0.0, 118.0},
	{2920.0, 66.0, 128.0, 262.0},
	{3060.0, 62.0, 0.0, 140.0},
	{3580.0, 60.0, 0.0, 96.0},
	{3580.0, 60.0, 166.0, 224.0},
	{3580.0, 60.0, 294.0, worldHeight},
	{4260.0, 62.0, 232.0, worldHeight},
	{4345.0, 70.0, 96.0, 150.0},
	{5420.0, 70.0, 112.0, 160.0},
	{5420.0, 70.0, 230.0, 278.0},
	{5560.0, 60.0, 0.0, 130.0},
	{5900.0, 56.0, 250.0, worldHeight},
	{5990.0, 56.0, 0.0, 140.0},
	{6080.0, 56.0, 250.0, worldHeight},
	{6950.0, 64.0, 150.0, 240.0},
	{7050.0, 62.0, 260.0, worldHeight},
	{7560.0, 60.0, 0.0, 110.0},
	{7560.0, 60.0, 180.0, 210.0},
	{7560.0, 60.0, 280.0, worldHeight},
	{8250.0, 62.0, 0.0, 150.0},
	{8330.0, 70.0, 232.0, 290.0},
	{8760.0, 62.0, 240.0, worldHeight},
	{8840.0, 70.0, 100.0, 155.0},
	{10100.0, 62.0, 0.0, 150.0},
	{10180.0, 70.0, 232.0, 287.0},
	{10600.0, 62.0, 240.0, worldHeight},
	{10680.0, 70.0, 103.0, 158.0},
	{11100.0, 58.0, 0.0, 118.0},
	{11150.0, 64.0, 172.0, 218.0},
	{11200.0, 58.0, 272.0, worldHeight},
	{11750.0, 60.0, 0.0, 96.0},
	{11750.0, 60.0, 166.0, 224.0},
	{11750.0, 60.0, 294.0, worldHeight},
	{12450.0, 56.0, 250.0, worldHeight},
	{12540.0, 56.0, 0.0, 140.0},
	{12630.0, 56.0, 250.0, worldHeight},
	{13050.0, 70.0, 112.0, 160.0},
	{13050.0, 70.0, 230.0, 278.0},
	{13190.0, 60.0, 0.0, 130.0},
	{13850.0, 58.0, 272.0, worldHeight},
	{13900.0, 64.0, 172.0, 218.0},
	{13950.0, 58.0, 0.0, 118.0},
	{14450.0, 66.0, 128.0, 262.0},
	{14590.0, 62.0, 0.0, 140.0},
	{15050.0, 62.0, 0.0, 150.0},
	{15130.0, 70.0, 232.0, 290.0},
	{16470.0, 62.0, 0.0, 150.0},
	{16550.0, 70.0, 232.0, 287.0},
	{16900.0, 62.0, 232.0, worldHeight},
	{16985.0, 70.0, 96.0, 150.0},
	{17350.0, 60.0, 0.0, 96.0},
	{17350.0, 60.0, 166.0, 224.0},
	{17350.0, 60.0, 294.0, worldHeight},
	{17850.0, 58.0, 0.0, 118.0},
	{17900.0, 64.0, 172.0, 218.0},
	{17950.0, 58.0, 272.0, worldHeight},
	{18360.0, 56.0, 250.0, worldHeight},
	{18450.0, 56.0, 0.0, 140.0},
	{18540.0, 56.0, 250.0, worldHeight},
	{18800.0, 64.0, 150.0, 240.0},
	{18900.0, 62.0, 260.0, worldHeight},
	{19420.0, 70.0, 112.0, 160.0},
	{19420.0, 70.0, 230.0, 278.0},
	{19560.0, 60.0, 0.0, 130.0},
	{19950.0, 62.0, 240.0, worldHeight},
	{20030.0, 70.0, 100.0, 155.0},
}

var standardGaps = [...]gap{
	{id: 0, minX: 3820.0, maxX: 4010.0, rail: GravityDown},
	{id: 1, minX: 4580.0, maxX: 4770.0, rail: GravityUp},
	{id: 2, minX: 6520.0, maxX: 6710.0, rail: GravityDown},
	{id: 3, minX: 7800.0, maxX: 7990.0, rail: GravityUp},
	{id: 4, minX: 12000.0, maxX: 12190.0, rail: GravityDown},
	{id: 5, minX: 13420.0, maxX: 13610.0, rail: GravityUp},
	{id: 6, minX: 17480.0, maxX: 17670.0, rail: GravityDown},
	{id: 7, minX: 19040.0, maxX: 19230.0, rail: GravityUp},
}

var standardPortals = [...]portal{
	{id: 0, x: 2190.0, rail: GravityDown, exitX: 1970.0},
	{id: 1, x: 3330.0, rail: GravityUp, exitX: 3110.0},
	{id: 2, x: 5150.0, rail: GravityDown, exitX: 4930.0},
	{id: 3, x: 7260.0, rail: GravityUp, exitX: 7040.0},
	{id: 4, x: 11450.0, rail: GravityDown, exitX: 11350.0},
	{id: 5, x: 14200.0, rail: GravityUp, exitX: 14100.0},
	{id: 6, x: 18220.0, rail: GravityDown, exitX: 18000.0},
	{id: 7, x: 19830.0, rail: GravityUp, exitX: 19610.0},
}

// standardSlows: a pad on a rail has onRail set and its y follows the rail
// surface (FausSwitchSlowPad.onRail); a floating pad has its own y.
var standardSlows = [...]slowSource{
	{id: 0, x: 1200.0, onRail: GravityDown},
	{id: 1, x: 2330.0, onRail: GravityUp},
	{id: 2, x: 4100.0, onRail: GravityDown},
	{id: 3, x: 4880.0, onRail: GravityUp},
	{id: 4, x: 6170.0, y: 205.0},
	{id: 5, x: 7380.0, onRail: GravityDown},
	{id: 6, x: 8080.0, onRail: GravityUp},
	{id: 7, x: 12270.0, onRail: GravityDown},
	{id: 8, x: 13310.0, y: 195.0},
	{id: 9, x: 13690.0, onRail: GravityUp},
	{id: 10, x: 14320.0, onRail: GravityDown},
	{id: 11, x: 17750.0, onRail: GravityDown},
	{id: 12, x: 19310.0, onRail: GravityUp},
	{id: 13, x: 19700.0, y: 200.0},
}

var standardBoosts = [...]pad{
	{id: 0, x: 1660.0, y: 150.0},
	{id: 1, x: 2720.0, y: 230.0},
	{id: 2, x: 5170.0, y: 190.0},
	{id: 3, x: 6300.0, y: 140.0},
	{id: 4, x: 8520.0, y: 250.0},
	{id: 5, x: 12900.0, y: 200.0},
	{id: 6, x: 14850.0, y: 150.0},
	{id: 7, x: 16700.0, y: 200.0},
	{id: 8, x: 18670.0, y: 230.0},
}
