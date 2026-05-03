package sema

import (
	"strings"

	"github.com/bholten/tools/idlc-go/internal/parser"
)

// legacyRPCSeeds maps the *first* RPC method of a class (by qualified
// "Package.Class.method(IDLType,...)") to the seed value the JAR emits
// in the RPC enum. Only some classes have a legacy seed — the JAR's
// formula is unknown, so we extract them by running the JAR over the
// upstream Core3 tree and reading back the first enum value. Anything
// not in this table emits no explicit value, and the C++ enum auto-
// numbers from 0.
//
// Regenerate via:
//
//	make baseline-jar
//	go run ./cmd/extract-seeds > /tmp/seeds.txt
//
// then replace the block below.
var legacyRPCSeeds = map[string]uint32{
	"engine.core.ManagedObject.updateForWrite()":                                                                                             3653780595,
	"engine.util.Facade.initializeSession()":                                                                                                 2342040833,
	"engine.util.Observable.notifyObservers(unsigned int,ManagedObject,long)":                                                                3221949456,
	"engine.util.Observer.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                                                   3437531614,
	"server.chat.ChatManager.stop()":                                                                                                         3192532258,
	"server.chat.ChatMessage.setString(string)":                                                                                              3293646548,
	"server.chat.PersistentMessage.sendTo(CreatureObject,boolean)":                                                                           5714349,
	"server.chat.room.ChatRoom.init(ZoneServer,ChatRoom,string)":                                                                             1944964435,
	"server.login.LoginServer.initializeTransientMembers()":                                                                                  2339364972,
	"server.login.account.Account.initializeTransientMembers()":                                                                              2608110191,
	"server.utils.LambdaObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                                            763076846,
	"server.zone.GroundZone.createContainerComponent()":                                                                                      69955860,
	"server.zone.SpaceZone.createContainerComponent()":                                                                                       687852154,
	"server.zone.TreeEntry.addInRangeObject(TreeEntry,boolean)":                                                                              4265233300,
	"server.zone.Zone.createContainerComponent()":                                                                                            2833757774,
	"server.zone.ZoneClientSession.disconnect()":                                                                                             1805730903,
	"server.zone.ZoneProcessServer.initialize()":                                                                                             1554126590,
	"server.zone.ZoneServer.initializeTransientMembers()":                                                                                    969596319,
	"server.zone.manager.ZoneManager.setZoneProcessor(ZoneProcessServer)":                                                                    390616416,
	"server.zone.managers.auction.AuctionManager.initialize()":                                                                               1052585454,
	"server.zone.managers.auction.AuctionsMap.addItem(CreatureObject,SceneObject,AuctionItem)":                                               1493191660,
	"server.zone.managers.city.CityManager.loadLuaConfig()":                                                                                  4105559252,
	"server.zone.managers.creature.CreatureManager.initialize()":                                                                             3210952586,
	"server.zone.managers.creature.DynamicSpawnObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                     606947208,
	"server.zone.managers.creature.LairObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                             2459089691,
	"server.zone.managers.creature.PetManager.initialize()":                                                                                  3384801361,
	"server.zone.managers.creature.SpawnObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                            3101886807,
	"server.zone.managers.creature.observers.CreatureHerdObserver.setSpacingBuffer(float)":                                                   2305331336,
	"server.zone.managers.director.QuestStatus.getKey()":                                                                                     3126513258,
	"server.zone.managers.director.QuestVectorMap.getKey()":                                                                                  21606873,
	"server.zone.managers.director.ScreenPlayObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                       1468633181,
	"server.zone.managers.frs.ArenaChallengeData.getChallengeStart()":                                                                        3934489673,
	"server.zone.managers.frs.ChallengeVoteData.addYesVote(unsigned long)":                                                                   3342400249,
	"server.zone.managers.frs.FrsManager.initialize()":                                                                                       2717747282,
	"server.zone.managers.frs.FrsRank.getRank()":                                                                                             2006151079,
	"server.zone.managers.gcw.GCWBaseShutdownObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                       3931201542,
	"server.zone.managers.gcw.GCWManager.getZone()":                                                                                          3465843338,
	"server.zone.managers.gcw.observers.ProbotObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                      1440679449,
	"server.zone.managers.gcw.sessions.ContrabandScanSession.initializeSession()":                                                            414740677,
	"server.zone.managers.gcw.sessions.WildContrabandScanSession.initializeSession()":                                                        1375124193,
	"server.zone.managers.guild.GuildManager.setChatManager(ChatManager)":                                                                    4202053867,
	"server.zone.managers.loot.LootManager.initialize()":                                                                                     2917100624,
	"server.zone.managers.minigames.FishingManager.initialize()":                                                                             936445983,
	"server.zone.managers.minigames.ForageManager.deleteForageAreaCollection(string)":                                                        294497697,
	"server.zone.managers.objectcontroller.ObjectController.finalize()":                                                                      3195147936,
	"server.zone.managers.player.PlayerManager.loadNameMap()":                                                                                2324343300,
	"server.zone.managers.radial.RadialManager.handleObjectMenuSelect(CreatureObject,byte,unsigned long)":                                    618011503,
	"server.zone.managers.reaction.ReactionManager.getReactionLevel(string)":                                                                 913684871,
	"server.zone.managers.resource.InterplanetarySurvey.getTimeStamp()":                                                                      308824843,
	"server.zone.managers.resource.ResourceManager.stop()":                                                                                   1114213504,
	"server.zone.managers.ship.SpaceSpawnObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                           1095229787,
	"server.zone.managers.weather.WeatherManager.initialize()":                                                                               2182280198,
	"server.zone.objects.area.ActiveArea.sendTo(SceneObject,boolean,boolean)":                                                                1878528101,
	"server.zone.objects.area.BadgeActiveArea.notifyEnter(SceneObject)":                                                                      790259300,
	"server.zone.objects.area.CampSiteActiveArea.initializeTransientMembers()":                                                               1029739918,
	"server.zone.objects.area.CampSiteObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                              409021232,
	"server.zone.objects.area.FsVillageArea.notifyEnter(SceneObject)":                                                                        312141493,
	"server.zone.objects.area.MissionReconActiveArea.notifyEnter(SceneObject)":                                                               3835253602,
	"server.zone.objects.area.MissionSpawnActiveArea.notifyEnter(SceneObject)":                                                               2622970179,
	"server.zone.objects.area.SarlaccArea.updateEruptTime()":                                                                                 3561429432,
	"server.zone.objects.area.areashapes.AreaShape.setAreaCenter(float,float)":                                                               2391961143,
	"server.zone.objects.area.areashapes.CircularAreaShape.setRadius(float)":                                                                 3347330704,
	"server.zone.objects.area.areashapes.CuboidAreaShape.setDimensions(float,float,float)":                                                   3956787085,
	"server.zone.objects.area.areashapes.RectangularAreaShape.setDimensions(float,float,float,float)":                                        4141362759,
	"server.zone.objects.area.areashapes.RingAreaShape.setInnerRadius(float)":                                                                1433668276,
	"server.zone.objects.area.areashapes.SphereAreaShape.setRadius(float)":                                                                   398174314,
	"server.zone.objects.area.space.NebulaArea.setNebulaDensity(float)":                                                                      1994890698,
	"server.zone.objects.area.space.SpaceActiveArea.notifyEnter(SceneObject)":                                                                1610933816,
	"server.zone.objects.auction.AuctionItem.initializeTransientMembers()":                                                                   276266056,
	"server.zone.objects.building.BuildingObject.createCellObjects()":                                                                        3291165931,
	"server.zone.objects.building.PoiBuilding.getNumberOfPlayersInRange()":                                                                   577384991,
	"server.zone.objects.building.TutorialBuildingObject.setTutorialOwnerID(unsigned long)":                                                  1263771599,
	"server.zone.objects.building.hospital.HospitalBuildingObject.isHospitalBuildingObject()":                                                3082759016,
	"server.zone.objects.building.recreation.RecreationBuildingObject.isRecreationalBuildingObject()":                                        2450418991,
	"server.zone.objects.creature.CreatureObject.initializeMembers()":                                                                        29990564,
	"server.zone.objects.creature.ai.AiAgent.initializeTransientMembers()":                                                                   24868240,
	"server.zone.objects.creature.ai.Creature.initializeTransientMembers()":                                                                  4015475806,
	"server.zone.objects.creature.ai.DroidObject.initializeTransientMembers()":                                                               2655348246,
	"server.zone.objects.creature.ai.NonPlayerCreatureObject.initializeTransientMembers()":                                                   1792645783,
	"server.zone.objects.creature.buffs.Buff.initializeTransientMembers()":                                                                   837587754,
	"server.zone.objects.creature.buffs.ChannelForceBuff.initializeTransientMembers()":                                                       878325981,
	"server.zone.objects.creature.buffs.ConcealBuff.getBuffGiver()":                                                                          3524884704,
	"server.zone.objects.creature.buffs.DurationBuff.activate(boolean)":                                                                      2417367960,
	"server.zone.objects.creature.buffs.ForceWeakenDebuff.initializeTransientMembers()":                                                      1913121489,
	"server.zone.objects.creature.buffs.GallopBuff.activate(boolean)":                                                                        425045282,
	"server.zone.objects.creature.buffs.PerformanceBuff.activate(boolean)":                                                                   1740866512,
	"server.zone.objects.creature.buffs.PlayerVehicleBuff.applyAllModifiers()":                                                               2932949861,
	"server.zone.objects.creature.buffs.PowerBoostBuff.initializeTransientMembers()":                                                         1186012662,
	"server.zone.objects.creature.buffs.PrivateBuff.activate(boolean)":                                                                       1987509396,
	"server.zone.objects.creature.buffs.PrivateSkillMultiplierBuff.applySkillModifiers()":                                                    2405941101,
	"server.zone.objects.creature.buffs.SingleUseBuffObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":               114056487,
	"server.zone.objects.creature.buffs.SpiceBuff.activate(boolean)":                                                                         3268252920,
	"server.zone.objects.creature.buffs.SpiceDownerBuff.activate(boolean)":                                                                   359527136,
	"server.zone.objects.creature.buffs.SquadLeaderBuff.finalize()":                                                                          49203159,
	"server.zone.objects.creature.buffs.SquadLeaderBuffObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":             2569662509,
	"server.zone.objects.creature.buffs.StateBuff.activate(boolean)":                                                                         741042654,
	"server.zone.objects.creature.buffs.TrapBuff.activate(boolean)":                                                                          3882889176,
	"server.zone.objects.creature.conversation.ConversationObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":         2960835814,
	"server.zone.objects.draftschematic.DraftSchematic.initializeTransientMembers()":                                                         237642605,
	"server.zone.objects.group.GroupObject.sendBaselinesTo(SceneObject)":                                                                     1552788934,
	"server.zone.objects.guild.GuildObject.initializeTransientMembers()":                                                                     178765840,
	"server.zone.objects.installation.InstallationObject.initializeTransientMembers()":                                                       3858591664,
	"server.zone.objects.installation.TurretObject.initializeTransientMembers()":                                                             628943437,
	"server.zone.objects.installation.TurretObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                        875304273,
	"server.zone.objects.installation.factory.FactoryHopperObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":         2663347641,
	"server.zone.objects.installation.garage.GarageInstallation.createChildObjects()":                                                        4263065178,
	"server.zone.objects.installation.harvester.HarvesterObject.setSelfPowered(boolean)":                                                     3970078830,
	"server.zone.objects.installation.shuttle.ShuttleInstallation.checkRequisitesForPlacement(CreatureObject)":                               88316350,
	"server.zone.objects.intangible.ControlDevice.updateToDatabaseAllObjects(boolean)":                                                       1886410738,
	"server.zone.objects.intangible.IntangibleObject.finalize()":                                                                             3178238000,
	"server.zone.objects.intangible.PetControlDevice.storeObject(CreatureObject,boolean)":                                                    1584499549,
	"server.zone.objects.intangible.PetControlObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                      1124304831,
	"server.zone.objects.intangible.ShipControlDevice.launchShip(CreatureObject,string,Vector3)":                                             294561104,
	"server.zone.objects.intangible.TheaterObject.getNumberOfPlayersInRange()":                                                               1917931802,
	"server.zone.objects.intangible.VehicleControlDevice.storeObject(CreatureObject,boolean)":                                                203423138,
	"server.zone.objects.intangible.VehicleControlObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                  1412597874,
	"server.zone.objects.manufactureschematic.ManufactureSchematic.initializeTransientMembers()":                                             3732900343,
	"server.zone.objects.mission.BountyMissionObjective.finalize()":                                                                          2413685197,
	"server.zone.objects.mission.CraftingMissionObjective.finalize()":                                                                        3358219931,
	"server.zone.objects.mission.DeliverMissionObjective.finalize()":                                                                         1114407076,
	"server.zone.objects.mission.DestroyMissionLairObserver.checkForHeal(TangibleObject,TangibleObject,boolean)":                             3181248478,
	"server.zone.objects.mission.DestroyMissionObjective.finalize()":                                                                         4145755434,
	"server.zone.objects.mission.EntertainerMissionObjective.finalize()":                                                                     1470878905,
	"server.zone.objects.mission.HuntingMissionObjective.finalize()":                                                                         2185561123,
	"server.zone.objects.mission.MissionObjective.initializeTransientMembers()":                                                              1042134107,
	"server.zone.objects.mission.MissionObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                            1471283440,
	"server.zone.objects.mission.PlayerBounty.setReward(int)":                                                                                2906054437,
	"server.zone.objects.mission.ReconMissionObjective.finalize()":                                                                           1869515498,
	"server.zone.objects.mission.SurveyMissionObjective.finalize()":                                                                          1714046748,
	"server.zone.objects.player.EntertainingObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                        2648784939,
	"server.zone.objects.player.PlayerObject.finalize()":                                                                                     594400956,
	"server.zone.objects.player.sessions.CityRemoveMilitiaSession.getMilitiaID()":                                                            1585133605,
	"server.zone.objects.player.sessions.CitySpecializationSession.initializeSession()":                                                      2099580494,
	"server.zone.objects.player.sessions.CityTreasuryWithdrawalSession.setReason(string)":                                                    2015667219,
	"server.zone.objects.player.sessions.DestroyStructureSession.isDestroyCode(unsigned int)":                                                474529412,
	"server.zone.objects.player.sessions.DroidMaintenanceSession.initialize()":                                                               3330724904,
	"server.zone.objects.player.sessions.EntertainingSession.doEntertainerPatronEffects()":                                                   2018339486,
	"server.zone.objects.player.sessions.FindSession.initializeSession()":                                                                    1060689262,
	"server.zone.objects.player.sessions.ImageDesignPositionObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":        3986841232,
	"server.zone.objects.player.sessions.ImageDesignSession.initializeTransientMembers()":                                                    3839124790,
	"server.zone.objects.player.sessions.InterplanetarySurveyDroidSession.setPlanet(string)":                                                 451335763,
	"server.zone.objects.player.sessions.LootLotterySession.initializeSession()":                                                             2137486503,
	"server.zone.objects.player.sessions.MigrateStatsSession.initializeSession()":                                                            1654755279,
	"server.zone.objects.player.sessions.PlaceStructureSession.initializeSession()":                                                          497288393,
	"server.zone.objects.player.sessions.ProposeUnitySession.getAskingPlayer()":                                                              4219049739,
	"server.zone.objects.player.sessions.StructureSetAccessFeeSession.initializeSession()":                                                   596554260,
	"server.zone.objects.player.sessions.TradeSession.getAcceptedTrade()":                                                                    1291877752,
	"server.zone.objects.player.sessions.VeteranRewardSession.getMilestone()":                                                                1973033673,
	"server.zone.objects.player.sessions.admin.PlayerManagementSession.initializeSession()":                                                  1653682681,
	"server.zone.objects.player.sessions.crafting.CraftingSession.initializeSession(CraftingTool,CraftingStation)":                           932433072,
	"server.zone.objects.player.sessions.survey.SurveySession.initializeSession(SurveyTool)":                                                 448820316,
	"server.zone.objects.player.sessions.vendor.CreateVendorSession.initializeSession()":                                                     3279166334,
	"server.zone.objects.player.sessions.vendor.NpcActorCreationSession.initializeSession()":                                                 2723647563,
	"server.zone.objects.player.sessions.vendor.VendorAdBarkingSession.initializeSession()":                                                  491494868,
	"server.zone.objects.player.sui.SuiBox.initialize()":                                                                                     277110457,
	"server.zone.objects.player.sui.banktransferbox.SuiBankTransferBox.addCash(int)":                                                         2418541022,
	"server.zone.objects.player.sui.listbox.SuiListBox.init()":                                                                               1325352454,
	"server.zone.objects.player.sui.listbox.SuiListBoxMenuItem.getObjectID()":                                                                2025223705,
	"server.zone.objects.player.sui.slotmachinebox.SuiSlotMachineBox.getPayoutBoxID()":                                                       413154677,
	"server.zone.objects.player.sui.transferbox.SuiFireworkDelayBox.getFireworkIndex()":                                                      4057308607,
	"server.zone.objects.region.CityRegion.initialize()":                                                                                     3487520683,
	"server.zone.objects.region.Region.setCityRegion(CityRegion)":                                                                            3495245448,
	"server.zone.objects.region.SpawnArea.notifyPositionUpdate(TreeEntry)":                                                                   4164633973,
	"server.zone.objects.region.SpawnAreaObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                           3913239956,
	"server.zone.objects.region.space.SpaceRegion.notifyLoadFromDatabase()":                                                                  1156896984,
	"server.zone.objects.region.space.SpaceSpawnArea.notifyPositionUpdate(TreeEntry)":                                                        3616855432,
	"server.zone.objects.region.space.SpaceSpawnAreaObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                717009547,
	"server.zone.objects.resource.ResourceContainer.initializeTransientMembers()":                                                            1134655640,
	"server.zone.objects.resource.ResourceSpawn.finalize()":                                                                                  889586298,
	"server.zone.objects.scene.SceneObject.finalize()":                                                                                       3521407902,
	"server.zone.objects.ship.PobShipObject.notifyLoadFromDatabase()":                                                                        675528490,
	"server.zone.objects.ship.ShipObject.finalize()":                                                                                         2034796776,
	"server.zone.objects.ship.ai.CapitalShipObject.getOutOfRangeDistance(unsigned long)":                                                     2478687471,
	"server.zone.objects.ship.ai.SpaceStationObject.getOutOfRangeDistance(unsigned long)":                                                    1744118955,
	"server.zone.objects.ship.components.ShipBoosterComponent.getBoosterAcceleration()":                                                      1035601341,
	"server.zone.objects.ship.components.ShipCapacitorComponent.getCapacitorEnergy()":                                                        2080416934,
	"server.zone.objects.ship.components.ShipChassisComponent.getCertificationRequired()":                                                    99585665,
	"server.zone.objects.ship.components.ShipComponent.getComponentDataName()":                                                               2571796163,
	"server.zone.objects.ship.components.ShipCounterMeasureComponent.getEffectivenessMin()":                                                  3337695987,
	"server.zone.objects.ship.components.ShipDroidInterfaceComponent.getDroidCommandSpeed()":                                                 1537178184,
	"server.zone.objects.ship.components.ShipEngineComponent.getAccelerationRate()":                                                          525293176,
	"server.zone.objects.ship.components.ShipMissileComponent.getMaxDamage()":                                                                3757134550,
	"server.zone.objects.ship.components.ShipReactorComponent.getReactorGenerationRate()":                                                    3017350929,
	"server.zone.objects.ship.components.ShipShieldComponent.getRearHitpoints()":                                                             3681880449,
	"server.zone.objects.ship.components.ShipWeaponComponent.getMaxDamage()":                                                                 3903786447,
	"server.zone.objects.staticobject.AsteroidObject.getOutOfRangeDistance(unsigned long)":                                                   1475182214,
	"server.zone.objects.staticobject.SpaceObject.getOutOfRangeDistance(unsigned long)":                                                      1539087714,
	"server.zone.objects.structure.StructureObject.initializeTransientMembers()":                                                             3783376556,
	"server.zone.objects.tangible.Instrument.initializeTransientMembers()":                                                                   2670500855,
	"server.zone.objects.tangible.JukeboxObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                           1851789773,
	"server.zone.objects.tangible.LairObject.getNumberOfPlayersInRange()":                                                                    764880985,
	"server.zone.objects.tangible.TangibleObject.initializeMembers()":                                                                        3335659148,
	"server.zone.objects.tangible.attachement.Attachment.initializeTransientMembers()":                                                       4007443492,
	"server.zone.objects.tangible.component.Component.initializeTransientMembers()":                                                          3918114691,
	"server.zone.objects.tangible.component.De10BarrelComponent.initializeTransientMembers()":                                                4091400068,
	"server.zone.objects.tangible.component.armor.ArmorComponent.initializeTransientMembers()":                                               1228203925,
	"server.zone.objects.tangible.component.dna.DnaComponent.setStats(float,float,float,float,float,float,float,float,float,float)":          3823629738,
	"server.zone.objects.tangible.component.droid.DroidComponent.initializeTransientMembers()":                                               662433442,
	"server.zone.objects.tangible.component.genetic.GeneticComponent.setSpecialResist(unsigned int)":                                         245972379,
	"server.zone.objects.tangible.component.lightsaber.LightsaberCrystalComponent.initializeTransientMembers()":                              1088783342,
	"server.zone.objects.tangible.components.droid.DroidHarvestObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":     2426178520,
	"server.zone.objects.tangible.components.droid.DroidPersonalityObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)": 2585724761,
	"server.zone.objects.tangible.components.droid.DroidPlaybackObserver.setSlot(int)":                                                       2217904037,
	"server.zone.objects.tangible.consumable.Consumable.handleObjectMenuSelect(CreatureObject,byte)":                                         877540444,
	"server.zone.objects.tangible.consumable.DelayedBuffObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":            1712113196,
	"server.zone.objects.tangible.consumable.Drink.initializeTransientMembers()":                                                             630441367,
	"server.zone.objects.tangible.consumable.Food.initializeTransientMembers()":                                                              204772233,
	"server.zone.objects.tangible.deed.Deed.initializeTransientMembers()":                                                                    2548063035,
	"server.zone.objects.tangible.deed.NavicomputerDeed.initializeTransientMembers()":                                                        3174001362,
	"server.zone.objects.tangible.deed.ResourceDeed.initializeTransientMembers()":                                                            3768260266,
	"server.zone.objects.tangible.deed.ShipDeed.initializeTransientMembers()":                                                                360569792,
	"server.zone.objects.tangible.deed.droid.DroidDeed.onCloneObject(SceneObject)":                                                           493480336,
	"server.zone.objects.tangible.deed.eventperk.EventPerkDeed.initializeTransientMembers()":                                                 716999798,
	"server.zone.objects.tangible.deed.pet.PetDeed.setSpecialResist(unsigned int)":                                                           3403443374,
	"server.zone.objects.tangible.deed.resource.VetHarvesterDeed.initializeTransientMembers()":                                               1647177094,
	"server.zone.objects.tangible.deed.vehicle.VehicleDeed.initializeTransientMembers()":                                                     2449348908,
	"server.zone.objects.tangible.eventperk.FlagGame.initializeTransientMembers()":                                                           4164117474,
	"server.zone.objects.tangible.eventperk.Jukebox.initializeTransientMembers()":                                                            780721685,
	"server.zone.objects.tangible.eventperk.LotteryDroid.initializeTransientMembers()":                                                       1062444868,
	"server.zone.objects.tangible.eventperk.ScavengerChest.isEventPerkItem()":                                                                1254428940,
	"server.zone.objects.tangible.eventperk.ScavengerDroid.handleObjectMenuSelect(CreatureObject,byte)":                                      2018248544,
	"server.zone.objects.tangible.eventperk.ShuttleBeacon.initializeTransientMembers()":                                                      64217072,
	"server.zone.objects.tangible.firework.FireworkObject.initializeTransientMembers()":                                                      692104112,
	"server.zone.objects.tangible.fishing.FishObject.initializeTransientMembers()":                                                           1950784664,
	"server.zone.objects.tangible.fishing.FishingBaitObject.initializeTransientMembers()":                                                    864827427,
	"server.zone.objects.tangible.fishing.FishingPoleObject.initializeTransientMembers()":                                                    711943701,
	"server.zone.objects.tangible.fscsobject.FsCsObject.initializeTransientMembers()":                                                        3030060280,
	"server.zone.objects.tangible.loot.LootkitObject.initializeTransientMembers()":                                                           1353252600,
	"server.zone.objects.tangible.misc.DroidProgrammingChip.initializeMembers()":                                                             743683302,
	"server.zone.objects.tangible.misc.LightObject.initializeMembers()":                                                                      3271836258,
	"server.zone.objects.tangible.misc.PlantObject.initializeTransientMembers()":                                                             713415823,
	"server.zone.objects.tangible.misc.SchematicFragment.initializeMembers()":                                                                277348600,
	"server.zone.objects.tangible.misc.ShipPaintKit.getPrimaryUsed()":                                                                        3649515406,
	"server.zone.objects.tangible.powerup.PowerupObject.isRanged()":                                                                          2188074194,
	"server.zone.objects.tangible.ship.interiorComponents.ShipInteriorComponent.setComponentSlot(int)":                                       325666000,
	"server.zone.objects.tangible.sign.SignObject.handleObjectMenuSelect(CreatureObject,byte)":                                               2964900160,
	"server.zone.objects.tangible.terminal.Terminal.initializeTransientMembers()":                                                            2743824535,
	"server.zone.objects.tangible.terminal.gambling.GamblingTerminal.initializeTransientMembers()":                                           3740544218,
	"server.zone.objects.tangible.terminal.guild.GuildTerminal.initializeTransientMembers()":                                                 3605692318,
	"server.zone.objects.tangible.terminal.spaceship.SpaceshipTerminal.handleObjectMenuSelect(CreatureObject,byte)":                          1763262837,
	"server.zone.objects.tangible.terminal.startinglocation.StartingLocationTerminal.initializeTransientMembers()":                           712503614,
	"server.zone.objects.tangible.terminal.ticketcollector.TicketCollector.initializeTransientMembers()":                                     3290238489,
	"server.zone.objects.tangible.terminal.travel.TravelTerminal.initializeTransientMembers()":                                               3275708814,
	"server.zone.objects.tangible.threat.ThreatMapObserver.notifyObserverEvent(unsigned int,Observable,ManagedObject,long)":                  14256476,
	"server.zone.objects.tangible.ticket.TicketObject.initializeTransientMembers()":                                                          1958512165,
	"server.zone.objects.tangible.tool.CraftingStation.initializeTransientMembers()":                                                         2544907394,
	"server.zone.objects.tangible.tool.CraftingTool.initializeTransientMembers()":                                                            1559421302,
	"server.zone.objects.tangible.tool.SurveyTool.initializeTransientMembers()":                                                              4161626280,
	"server.zone.objects.tangible.tool.ToolTangibleObject.initializeTransientMembers()":                                                      4144890609,
	"server.zone.objects.tangible.tool.antidecay.AntiDecayKit.initializeTransientMembers()":                                                  2429789154,
	"server.zone.objects.tangible.tool.componentanalysis.ComponentAnalysisTool.initializePrivateData()":                                      537953379,
	"server.zone.objects.tangible.tool.recycle.RecycleTool.initializeTransientMembers()":                                                     3179759582,
	"server.zone.objects.tangible.tool.repair.RepairTool.isRepairTool()":                                                                     4136788283,
	"server.zone.objects.tangible.tool.smuggler.PrecisionLaserKnife.handleObjectMenuSelect(CreatureObject,byte)":                             2447060878,
	"server.zone.objects.tangible.tool.smuggler.SlicingTool.handleObjectMenuSelect(CreatureObject,byte)":                                     980607666,
	"server.zone.objects.tangible.wearables.ArmorObject.initializeTransientMembers()":                                                        4036877556,
	"server.zone.objects.tangible.wearables.ClothingObject.initializeTransientMembers()":                                                     4211451250,
	"server.zone.objects.tangible.wearables.PsgArmorObject.initializeTransientMembers()":                                                     3719141856,
	"server.zone.objects.tangible.wearables.RobeObject.initializeTransientMembers()":                                                         2257510045,
	"server.zone.objects.tangible.wearables.WearableContainerObject.initializeTransientMembers()":                                            753592095,
	"server.zone.objects.tangible.wearables.WearableObject.initializeTransientMembers()":                                                     775195158,
	"testsuite3.tests.TestIDLClass.getValue()":                                                                                               1133365074,

	// Probe seeds — used by synthetic IDLs in testdata/probe/, not in
	// the Core3 corpus. Keep these alongside the extracted set so the
	// probe tests stay byte-identical when the file is regenerated.
	"probe.Locking.plain(int)":                   49071539,
	"probe.Returns.retInt()":                     664163704,
	"probe.Params.plainInt(int)":                 1117188990,
	"probe.Dispatch.plainNative()":               2935911594,
	"probe.Generics.retVecInt()":                 534223235,
	"probe.Inheritance.finalize()":               2585546652,
	"probe.NativeCtor.getValue()":                3307767849,
	"probe.RawDereferenced.getDereferencedRef()": 1517703269,
	"probe.Scriptable.isReady()":                 3020311965,
}

// RPCEnumMangle returns the type suffix used in the RPC enum constant
// (e.g. "STRING" in `RPC_SETSTRING__STRING_`, "LONG" in
// `RPC_ADDPENDINGMESSAGE__LONG_`). Distinct from the wire-format
// mangle in WireMangle.
func RPCEnumMangle(t parser.Type) string {
	// Class types and types not in the table fall through to a simple
	// uppercase of the unqualified name.
	switch t.Name {
	case "string":
		return "STRING"
	case "unicode":
		return "UNICODESTRING"
	case "boolean":
		return "BOOL"
	case "byte", "unsigned byte":
		return "BYTE"
	case "short", "unsigned short":
		return "SHORT"
	case "int", "unsigned int":
		return "INT"
	case "long", "unsigned long":
		return "LONG"
	case "float":
		return "FLOAT"
	}

	// User class. Generics are ignored in the RPC enum suffix.
	return strings.ToUpper(lastQNamePart(t.Name))
}

// WireInsertMangle is like WireMangle but follows a JAR quirk for the
// adapter's `resp->insertXxx(_m_res)` calls: unsigned widths collapse
// to their signed names (Int / Long / Short) — but `int` itself keeps
// the "SignedInt" mangle. Other types match WireMangle.
//
// Source: ManagedObject.cpp's adapter cases for getLastCRCSave (unsigned
// int → insertInt) vs getPersistenceLevel (int → insertSignedInt),
// ChatRoom.cpp's getOwnerID (unsigned long → insertLong), and
// ZoneClientSession.cpp's getPort (unsigned short → insertShort).
func WireInsertMangle(t parser.Type) string {
	switch t.Name {
	case "unsigned int":
		return "Int"
	case "unsigned long":
		return "Long"
	case "unsigned short":
		return "Short"
	}

	return WireMangle(t)
}

// WireMangle returns the suffix used in DistributedMethod calls
// (addXParameter / getXParameter / executeWithXReturn / insertX).
func WireMangle(t parser.Type) string {
	switch t.Name {
	case "string":
		return "Ascii"
	case "unicode":
		return "Unicode"
	case "boolean":
		return "Boolean"
	case "byte", "unsigned byte":
		return "Byte"
	case "short":
		return "SignedShort"
	case "unsigned short":
		return "UnsignedShort"
	case "int":
		return "SignedInt"
	case "unsigned int":
		return "UnsignedInt"
	case "long":
		return "SignedLong"
	case "unsigned long":
		return "UnsignedLong"
	case "float":
		return "Float"
	}

	// User class types take the Object marshalling path.
	return "Object"
}

// WireIsByRef reports whether the wire-format get pattern uses a
// reference parameter — e.g. `String x; inv->getAsciiParameter(x);` —
// rather than a value return — `int x = inv->getSignedIntParameter();`.
//
// String/Unicode/Object are by-ref; primitives are by-value.
func WireIsByRef(t parser.Type) bool {
	switch t.Name {
	case "string", "unicode":
		return true
	}

	return !IsPrimitive(t.Name)
}

// rpcSymbol builds the full RPC enum constant name for a method.
func rpcSymbol(methodName string, params []Param) string {
	var b strings.Builder
	b.WriteString("RPC_")
	b.WriteString(strings.ToUpper(methodName))
	b.WriteString("__")

	for _, p := range params {
		b.WriteString(RPCEnumMangle(p.IDLType))
		b.WriteString("_")
	}

	return b.String()
}

// legacySeedKey: "Package.Class.method(IDLType1,IDLType2)"
func legacySeedKey(pkg []string, className, methodName string, params []Param) string {
	var b strings.Builder
	b.WriteString(strings.Join(pkg, "."))
	b.WriteString(".")
	b.WriteString(className)
	b.WriteString(".")
	b.WriteString(methodName)
	b.WriteString("(")

	for i, p := range params {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(p.IDLType.Name)
	}

	b.WriteString(")")

	return b.String()
}

// LookupSeed returns the legacy JAR seed for this method, if known.
func LookupSeed(pkg []string, className, methodName string, params []Param) (uint32, bool) {
	v, ok := legacyRPCSeeds[legacySeedKey(pkg, className, methodName, params)]
	return v, ok
}
