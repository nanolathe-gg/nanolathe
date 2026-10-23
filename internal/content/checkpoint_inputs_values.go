package content

// Typed comparisons deliberately retain stored values, including derived
// flags and exact float bits. The rounded M2 canonical writers are unchanged
// (DESIGN_MULTIPLAYER §16.3.63). Provenance is host-local metadata.

func sameCheckpointInputHeader(a, b DefinitionHeader) bool {
	return a.CanonicalKey == b.CanonicalKey && a.Hash == b.Hash
}

func sameCheckpointInputUnit(a, b *UnitDef) bool {
	return sameCheckpointInputHeader(a.DefinitionHeader, b.DefinitionHeader) &&
		a.extensionKeys == b.extensionKeys &&
		a.DiscoveryOnly == b.DiscoveryOnly &&
		a.UnitDefID == b.UnitDefID &&
		a.UnitName == b.UnitName &&
		a.Name == b.Name &&
		a.Description == b.Description &&
		a.Side == b.Side &&
		a.ObjectName == b.ObjectName &&
		a.Category == b.Category &&
		a.SoundCategory == b.SoundCategory &&
		a.Corpse == b.Corpse &&
		a.MovementClass == b.MovementClass &&
		a.MobilityDomain == b.MobilityDomain &&
		a.Weapon1 == b.Weapon1 &&
		a.Weapon2 == b.Weapon2 &&
		a.Weapon3 == b.Weapon3 &&
		a.ExplodeAs == b.ExplodeAs &&
		a.SelfDestructAs == b.SelfDestructAs &&
		a.YardMap == b.YardMap &&
		a.DefaultMissionType == b.DefaultMissionType &&
		a.BadTargetCategoryWPRI == b.BadTargetCategoryWPRI &&
		a.BadTargetCategoryWSEC == b.BadTargetCategoryWSEC &&
		a.BadTargetCategoryWSPE == b.BadTargetCategoryWSPE &&
		a.NoChaseCategory == b.NoChaseCategory &&
		a.AIWeight == b.AIWeight &&
		a.Rotations == b.Rotations &&
		a.VeterancyAccuracyBuffRate == b.VeterancyAccuracyBuffRate &&
		a.TransportedExplodeAs == b.TransportedExplodeAs &&
		a.TransportedSelfDestructAs == b.TransportedSelfDestructAs &&
		a.PreviewPieces == b.PreviewPieces &&
		a.PreviewPiecesS == b.PreviewPiecesS &&
		a.PreviewPiecesE == b.PreviewPiecesE &&
		a.PreviewPiecesN == b.PreviewPiecesN &&
		a.PreviewPiecesW == b.PreviewPiecesW &&
		a.PreviewFaceOpponent == b.PreviewFaceOpponent &&
		a.PreviewObject3D == b.PreviewObject3D &&
		a.NanolatheInfector == b.NanolatheInfector &&
		a.BuilderSpeed == b.BuilderSpeed &&
		a.SmartMaker == b.SmartMaker &&
		checkpointInputF32(a.BuildCostEnergy, b.BuildCostEnergy) &&
		checkpointInputF32(a.BuildCostMetal, b.BuildCostMetal) &&
		checkpointInputF64(a.EnergyMake, b.EnergyMake) &&
		checkpointInputF64(a.EnergyUse, b.EnergyUse) &&
		checkpointInputF64(a.MetalMake, b.MetalMake) &&
		checkpointInputF64(a.ExtractsMetal, b.ExtractsMetal) &&
		checkpointInputF64(a.WindGenerator, b.WindGenerator) &&
		checkpointInputF64(a.TidalGenerator, b.TidalGenerator) &&
		checkpointInputF64(a.EnergyStorage, b.EnergyStorage) &&
		checkpointInputF64(a.MetalStorage, b.MetalStorage) &&
		a.MakesMetal == b.MakesMetal &&
		a.BuildTime == b.BuildTime &&
		a.WorkerTime == b.WorkerTime &&
		a.HealTime == b.HealTime &&
		a.CloakCost == b.CloakCost &&
		a.CloakCostMoving == b.CloakCostMoving &&
		a.MaxVelocity == b.MaxVelocity &&
		a.BrakeRate == b.BrakeRate &&
		a.Acceleration == b.Acceleration &&
		a.BankScale == b.BankScale &&
		a.PitchScale == b.PitchScale &&
		a.DamageModifier == b.DamageModifier &&
		a.MoveRate1 == b.MoveRate1 &&
		a.MoveRate2 == b.MoveRate2 &&
		a.TurnRate == b.TurnRate &&
		a.Waterline == b.Waterline &&
		a.MinWaterDepth == b.MinWaterDepth &&
		a.MaxWaterDepth == b.MaxWaterDepth &&
		a.MaxSlope == b.MaxSlope &&
		a.BadSlope == b.BadSlope &&
		a.MaxWaterSlope == b.MaxWaterSlope &&
		a.BadWaterSlope == b.BadWaterSlope &&
		a.CruiseAlt == b.CruiseAlt &&
		a.TransportSize == b.TransportSize &&
		a.TransportCapacity == b.TransportCapacity &&
		a.BuildAngle == b.BuildAngle &&
		a.BuildDistance == b.BuildDistance &&
		a.SortBias == b.SortBias &&
		a.ManeuverLeashLength == b.ManeuverLeashLength &&
		a.AttackRunLength == b.AttackRunLength &&
		a.KamikazeDistance == b.KamikazeDistance &&
		a.FootprintX == b.FootprintX &&
		a.FootprintZ == b.FootprintZ &&
		a.MaxDamage == b.MaxDamage &&
		a.SightDistance == b.SightDistance &&
		a.RadarDistance == b.RadarDistance &&
		a.SonarDistance == b.SonarDistance &&
		a.RadarDistanceJam == b.RadarDistanceJam &&
		a.SonarDistanceJam == b.SonarDistanceJam &&
		a.MinCloakDistance == b.MinCloakDistance &&
		a.ModelTop == b.ModelTop &&
		a.ModelTopFixed == b.ModelTopFixed &&
		a.BuildPageCount == b.BuildPageCount &&
		a.HasPageZeroGUI == b.HasPageZeroGUI &&
		a.StandingMoveOrder == b.StandingMoveOrder &&
		a.StandingFireOrder == b.StandingFireOrder &&
		a.InitCloaked == b.InitCloaked &&
		a.Downloadable == b.Downloadable &&
		a.Builder == b.Builder &&
		a.Stealth == b.Stealth &&
		a.BMCode == b.BMCode &&
		a.ZBuffer == b.ZBuffer &&
		a.IsAirBase == b.IsAirBase &&
		a.IsTargetingUpgrade == b.IsTargetingUpgrade &&
		a.Teleporter == b.Teleporter &&
		a.HideDamage == b.HideDamage &&
		a.ShootMe == b.ShootMe &&
		a.ArmoredState == b.ArmoredState &&
		a.ActivateWhenBuilt == b.ActivateWhenBuilt &&
		a.CanFly == b.CanFly &&
		a.CanHover == b.CanHover &&
		a.Upright == b.Upright &&
		a.Floater == b.Floater &&
		a.Amphibious == b.Amphibious &&
		a.IsFeature == b.IsFeature &&
		a.NoShadow == b.NoShadow &&
		a.ImmuneToParalyzer == b.ImmuneToParalyzer &&
		a.HoverAttack == b.HoverAttack &&
		a.AntiWeapons == b.AntiWeapons &&
		a.Digger == b.Digger &&
		a.OnOffable == b.OnOffable &&
		a.MobileStandOrders == b.MobileStandOrders &&
		a.FireStandOrders == b.FireStandOrders &&
		a.CanStop == b.CanStop &&
		a.CanAttack == b.CanAttack &&
		a.CanGuard == b.CanGuard &&
		a.CanPatrol == b.CanPatrol &&
		a.CanMove == b.CanMove &&
		a.CanLoad == b.CanLoad &&
		a.CanReclamate == b.CanReclamate &&
		a.CanResurrect == b.CanResurrect &&
		a.CanCapture == b.CanCapture &&
		a.CanDGun == b.CanDGun &&
		a.Kamikaze == b.Kamikaze &&
		a.NoRestrict == b.NoRestrict &&
		a.ShowPlayerName == b.ShowPlayerName &&
		a.Commander == b.Commander &&
		a.CantBeTransported == b.CantBeTransported &&
		a.Wacky == b.Wacky &&
		a.UnitLimit == b.UnitLimit &&
		a.LimitEnabled == b.LimitEnabled &&
		a.Limit == b.Limit &&
		a.SelfDestructCountdown == b.SelfDestructCountdown &&
		a.SelfDestructCountdownPresent == b.SelfDestructCountdownPresent &&
		a.Weapon1Def == b.Weapon1Def &&
		a.Weapon2Def == b.Weapon2Def &&
		a.Weapon3Def == b.Weapon3Def &&
		a.ExplodeAsDef == b.ExplodeAsDef &&
		a.SelfDestructAsDef == b.SelfDestructAsDef &&
		a.TransportedExplodeAsDef == b.TransportedExplodeAsDef &&
		a.TransportedSelfDestructAsDef == b.TransportedSelfDestructAsDef
}

func sameCheckpointInputWeapon(a, b *WeaponDef) bool {
	return sameCheckpointInputHeader(a.DefinitionHeader, b.DefinitionHeader) &&
		a.extensionKeys == b.extensionKeys &&
		a.activeByte == b.activeByte &&
		a.activeByteRestored == b.activeByteRestored &&
		a.ID == b.ID &&
		a.Name == b.Name &&
		a.WeaponVelocity == b.WeaponVelocity &&
		a.StartVelocity == b.StartVelocity &&
		a.WeaponAcceleration == b.WeaponAcceleration &&
		a.ReloadTime == b.ReloadTime &&
		a.WeaponTimer == b.WeaponTimer &&
		a.BurstRate == b.BurstRate &&
		a.Duration == b.Duration &&
		a.RandomDecay == b.RandomDecay &&
		a.SmokeDelay == b.SmokeDelay &&
		a.FlightTime == b.FlightTime &&
		a.HoldTime == b.HoldTime &&
		a.ShakeDuration == b.ShakeDuration &&
		a.TurnRate == b.TurnRate &&
		checkpointInputF64(a.MinBarrelAngle, b.MinBarrelAngle) &&
		a.Range == b.Range &&
		a.Coverage == b.Coverage &&
		a.AreaOfEffect == b.AreaOfEffect &&
		checkpointInputF64(a.EdgeEffectiveness, b.EdgeEffectiveness) &&
		checkpointInputF64(a.EnergyPerShot, b.EnergyPerShot) &&
		checkpointInputF64(a.MetalPerShot, b.MetalPerShot) &&
		a.Burst == b.Burst &&
		a.SprayAngle == b.SprayAngle &&
		a.Accuracy == b.Accuracy &&
		a.Tolerance == b.Tolerance &&
		a.PitchTolerance == b.PitchTolerance &&
		a.ShakeMagnitude == b.ShakeMagnitude &&
		a.Firestarter == b.Firestarter &&
		a.RenderType == b.RenderType &&
		a.Color == b.Color &&
		a.Color2 == b.Color2 &&
		a.NoAutoRange == b.NoAutoRange &&
		a.SoundTrigger == b.SoundTrigger &&
		a.Guidance == b.Guidance &&
		a.Tracks == b.Tracks &&
		a.LineOfSight == b.LineOfSight &&
		a.Ballistic == b.Ballistic &&
		a.UnitsOnly == b.UnitsOnly &&
		a.GroundBounce == b.GroundBounce &&
		a.WaterWeapon == b.WaterWeapon &&
		a.ToAirWeapon == b.ToAirWeapon &&
		a.SmokeTrail == b.SmokeTrail &&
		a.Turret == b.Turret &&
		a.SelfProp == b.SelfProp &&
		a.Propeller == b.Propeller &&
		a.NoExplode == b.NoExplode &&
		a.BurnBlow == b.BurnBlow &&
		a.TwoPhase == b.TwoPhase &&
		a.Cruise == b.Cruise &&
		a.CommandFire == b.CommandFire &&
		a.Stockpile == b.Stockpile &&
		a.Targetable == b.Targetable &&
		a.Interceptor == b.Interceptor &&
		a.BeamWeapon == b.BeamWeapon &&
		a.ShellWeapon == b.ShellWeapon &&
		a.Dropped == b.Dropped &&
		a.VLaunch == b.VLaunch &&
		a.Meteor == b.Meteor &&
		a.NoRadar == b.NoRadar &&
		a.Paralyzer == b.Paralyzer &&
		a.StartSmoke == b.StartSmoke &&
		a.EndSmoke == b.EndSmoke &&
		a.NotToAir == b.NotToAir &&
		a.ToAirOnly == b.ToAirOnly &&
		a.NotToUnderwater == b.NotToUnderwater &&
		a.SurfaceFire == b.SurfaceFire &&
		a.NoOverWater == b.NoOverWater &&
		a.NoOverLand == b.NoOverLand &&
		a.NoMapWeaponAlert == b.NoMapWeaponAlert &&
		a.ReloadBar == b.ReloadBar &&
		a.Model == b.Model &&
		a.ExplosionGaf == b.ExplosionGaf &&
		a.ExplosionArt == b.ExplosionArt &&
		a.WaterExplosionGaf == b.WaterExplosionGaf &&
		a.WaterExplosionArt == b.WaterExplosionArt &&
		a.LavaExplosionGaf == b.LavaExplosionGaf &&
		a.LavaExplosionArt == b.LavaExplosionArt &&
		a.SoundStart == b.SoundStart &&
		a.SoundHit == b.SoundHit &&
		a.SoundWater == b.SoundWater &&
		a.DamageDefault == b.DamageDefault
}

func sameCheckpointInputFeature(a, b *FeatureDef) bool {
	return sameCheckpointInputHeader(a.DefinitionHeader, b.DefinitionHeader) &&
		a.Description == b.Description &&
		a.FootprintX == b.FootprintX &&
		a.FootprintZ == b.FootprintZ &&
		a.Height == b.Height &&
		a.Object == b.Object &&
		a.Filename == b.Filename &&
		a.SeqName == b.SeqName &&
		a.SeqNameShad == b.SeqNameShad &&
		a.SeqNameBurn == b.SeqNameBurn &&
		a.SeqNameBurnShad == b.SeqNameBurnShad &&
		a.SeqNameDie == b.SeqNameDie &&
		a.SeqNameDieShad == b.SeqNameDieShad &&
		a.SeqNameReclamate == b.SeqNameReclamate &&
		a.SeqNameReclamateShad == b.SeqNameReclamateShad &&
		a.Metal == b.Metal &&
		a.Energy == b.Energy &&
		a.Damage == b.Damage &&
		a.SpreadChance == b.SpreadChance &&
		a.Reproduce == b.Reproduce &&
		a.ReproduceArea == b.ReproduceArea &&
		a.SparkTime == b.SparkTime &&
		a.BurnWeapon == b.BurnWeapon &&
		a.Animating == b.Animating &&
		a.AnimTrans == b.AnimTrans &&
		a.ShadTrans == b.ShadTrans &&
		a.Flamable == b.Flamable &&
		a.Geothermal == b.Geothermal &&
		a.Blocking == b.Blocking &&
		a.Reclaimable == b.Reclaimable &&
		a.Autoreclaimable == b.Autoreclaimable &&
		a.Indestructible == b.Indestructible &&
		a.NoDisplayInfo == b.NoDisplayInfo &&
		a.NoDrawUnderGray == b.NoDrawUnderGray &&
		a.FeatureDead == b.FeatureDead &&
		a.FeatureReclamate == b.FeatureReclamate &&
		a.FeatureBurnt == b.FeatureBurnt &&
		a.FeatureDeadDef == b.FeatureDeadDef &&
		a.FeatureReclamateDef == b.FeatureReclamateDef &&
		a.FeatureBurntDef == b.FeatureBurntDef
}

func sameCheckpointInputMovement(a, b *MovementClass) bool {
	return sameCheckpointInputHeader(a.DefinitionHeader, b.DefinitionHeader) &&
		a.FootprintX == b.FootprintX &&
		a.FootprintZ == b.FootprintZ &&
		a.MaxWaterDepth == b.MaxWaterDepth &&
		a.MinWaterDepth == b.MinWaterDepth &&
		a.MaxSlope == b.MaxSlope &&
		a.BadSlope == b.BadSlope &&
		a.MaxWaterSlope == b.MaxWaterSlope &&
		a.BadWaterSlope == b.BadWaterSlope
}

func sameCheckpointInputSide(a, b *SideDef) bool {
	return sameCheckpointInputHeader(a.DefinitionHeader, b.DefinitionHeader) &&
		a.Index == b.Index &&
		a.Name == b.Name &&
		a.NamePrefix == b.NamePrefix &&
		a.Commander == b.Commander &&
		a.IntGAF == b.IntGAF &&
		a.Font == b.Font &&
		a.FontGUI == b.FontGUI &&
		a.EnergyColor == b.EnergyColor &&
		a.MetalColor == b.MetalColor &&
		a.BaseHeight == b.BaseHeight
}

func sameCheckpointInputMeteor(a, b *MeteorDefaults) bool {
	return sameCheckpointInputHeader(a.DefinitionHeader, b.DefinitionHeader) &&
		a.DefaultPresent == b.DefaultPresent &&
		a.DefaultValid == b.DefaultValid &&
		a.MeteorWeapon == b.MeteorWeapon &&
		a.MeteorRadius == b.MeteorRadius &&
		checkpointInputF32(a.MeteorDensity, b.MeteorDensity) &&
		checkpointInputF32(a.MeteorDuration, b.MeteorDuration) &&
		checkpointInputF32(a.MeteorInterval, b.MeteorInterval)
}

func sameCheckpointInputMapHeader(a, b *MapHeader) bool {
	return sameCheckpointInputHeader(a.DefinitionHeader, b.DefinitionHeader) &&
		a.Name == b.Name &&
		a.LogicalOTA == b.LogicalOTA &&
		a.LogicalTNT == b.LogicalTNT &&
		a.MissionName == b.MissionName &&
		a.MissionDescription == b.MissionDescription &&
		a.Planet == b.Planet &&
		a.Memory == b.Memory &&
		a.NumPlayers == b.NumPlayers &&
		a.Size == b.Size &&
		a.Brief == b.Brief &&
		a.Narration == b.Narration &&
		a.MissionHint == b.MissionHint &&
		a.Glamour == b.Glamour &&
		a.GlamourSound == b.GlamourSound &&
		a.UseOnlyUnits == b.UseOnlyUnits &&
		a.LineOfSight == b.LineOfSight &&
		a.Mapping == b.Mapping &&
		a.NoMovie == b.NoMovie &&
		checkpointInputF64(a.TidalStrength, b.TidalStrength) &&
		a.SolarStrength == b.SolarStrength &&
		a.LavaWorld == b.LavaWorld &&
		a.WaterDoesDamage == b.WaterDoesDamage &&
		a.WaterDamage == b.WaterDamage &&
		a.NoSeaLevelTrigger == b.NoSeaLevelTrigger &&
		checkpointInputF64(a.KillMul, b.KillMul) &&
		checkpointInputF64(a.TimeMul, b.TimeMul) &&
		a.MinWindSpeed == b.MinWindSpeed &&
		a.MaxWindSpeed == b.MaxWindSpeed &&
		a.Gravity == b.Gravity &&
		a.MaxUnits == b.MaxUnits &&
		a.TNTWidth == b.TNTWidth &&
		a.TNTHeight == b.TNTHeight &&
		a.TNTSeaLevel == b.TNTSeaLevel &&
		a.TNTTiles == b.TNTTiles &&
		a.TNTTileAnims == b.TNTTileAnims &&
		a.MinimapWidth == b.MinimapWidth &&
		a.MinimapHeight == b.MinimapHeight &&
		a.TNTVersion == b.TNTVersion &&
		a.UnknownHeader1 == b.UnknownHeader1 &&
		a.GlobalHeader == b.GlobalHeader
}

func sameCheckpointInputMapSchema(a, b *MapSchema) bool {
	return a.Name == b.Name &&
		a.Type == b.Type &&
		a.AIProfile == b.AIProfile &&
		a.SurfaceMetal == b.SurfaceMetal &&
		a.MohoMetal == b.MohoMetal &&
		a.HumanMetal == b.HumanMetal &&
		a.HumanEnergy == b.HumanEnergy &&
		a.ComputerMetal == b.ComputerMetal &&
		a.ComputerEnergy == b.ComputerEnergy &&
		a.MeteorWeapon == b.MeteorWeapon &&
		a.MeteorRadius == b.MeteorRadius &&
		checkpointInputF64(a.MeteorDensity, b.MeteorDensity) &&
		checkpointInputF64(a.MeteorDuration, b.MeteorDuration) &&
		checkpointInputF64(a.MeteorInterval, b.MeteorInterval) &&
		a.StartPosCount == b.StartPosCount
}
