package migrations

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// systemChoreCatalogSeed adds the initial system chore catalog (source:
// external/doc/SYSTEM_CHORECATALOGS.json). A system entry stores localization
// keys as its title and description; the catalog query resolves them in the
// requesting user's language. Existing entries and translations are kept, so
// running it again changes nothing.
type systemChoreCatalogSeed struct{}

func (*systemChoreCatalogSeed) Version() string { return "0069" }
func (*systemChoreCatalogSeed) Name() string    { return "systemChoreCatalogSeed" }

// systemChoreCatalogKeyPrefix starts every system catalog localization key.
const systemChoreCatalogKeyPrefix = "choreCatalog.system."

type systemChoreCatalogEntry struct {
	// key is "<category>.<chore>"; title and description keys append ".title" and ".description".
	key                       string
	category, level, interval int
	trTitle, trDescription    string
	enTitle, enDescription    string
}

func (*systemChoreCatalogSeed) Up(ctx context.Context, db *mongo.Database) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	messages := []struct{ key, language, value string }{
		{"choreCatalog.error.system_requires_keys", "en", "System catalog entries must use localization keys starting with choreCatalog.system. as title and description."},
		{"choreCatalog.error.system_requires_keys", "tr", "Sistem katalog kayıtlarının başlık ve açıklaması choreCatalog.system. ile başlayan lokalizasyon anahtarları olmalıdır."},
	}
	for _, entry := range systemChoreCatalogEntries {
		titleKey := systemChoreCatalogKeyPrefix + entry.key + ".title"
		descriptionKey := systemChoreCatalogKeyPrefix + entry.key + ".description"
		messages = append(messages,
			struct{ key, language, value string }{titleKey, "tr", entry.trTitle},
			struct{ key, language, value string }{titleKey, "en", entry.enTitle},
			struct{ key, language, value string }{descriptionKey, "tr", entry.trDescription},
			struct{ key, language, value string }{descriptionKey, "en", entry.enDescription},
		)
		if _, err := db.Collection("ChoreCatalog").UpdateOne(ctx, bson.M{
			"userId": "SYSTEM", "title": titleKey,
		}, bson.M{"$setOnInsert": bson.M{
			"description":       descriptionKey,
			"recurringInterval": entry.interval,
			"level":             entry.level,
			"category":          entry.category,
			"createdOn":         now,
			"updatedOn":         now,
			"isDeleted":         false,
		}}, options.Update().SetUpsert(true)); err != nil {
			return err
		}
	}
	for _, message := range messages {
		if _, err := db.Collection("localization").UpdateOne(ctx, bson.M{
			"language": message.language,
			"type":     "message",
			"key":      message.key,
		}, bson.M{"$setOnInsert": bson.M{
			"value":     message.value,
			"createdOn": now,
			"updatedOn": now,
		}}, options.Update().SetUpsert(true)); err != nil {
			return err
		}
	}
	return nil
}

var systemChoreCatalogEntries = []systemChoreCatalogEntry{
	{"kitchen.wash_dishes", 1, 10, 1,
		"Bulaşıkları yıka", "Lavabodaki bulaşıkları yıka, durula ve kurumaya bırak.",
		"Wash the dishes", "Wash the dishes in the sink, rinse them and leave them to dry."},
	{"kitchen.load_dishwasher", 1, 10, 1,
		"Bulaşık makinesini doldur", "Kirli tabakları makineye diz, tableti koy ve çalıştır.",
		"Load the dishwasher", "Load the dirty dishes, add a tablet and start the dishwasher."},
	{"kitchen.unload_dishwasher", 1, 10, 1,
		"Bulaşık makinesini boşalt", "Temiz bulaşıkları dolaplara yerleştir.",
		"Unload the dishwasher", "Put the clean dishes away in the cupboards."},
	{"kitchen.cook_dinner", 1, 30, 1,
		"Akşam yemeğini hazırla", "Evin akşam yemeğini planla ve pişir.",
		"Cook dinner", "Plan and cook dinner for the household."},
	{"kitchen.make_breakfast", 1, 20, 1,
		"Kahvaltıyı hazırla", "Kahvaltı sofrasını kur, çay/kahveyi demle.",
		"Make breakfast", "Set the breakfast table and brew the tea or coffee."},
	{"kitchen.wipe_counters", 1, 10, 1,
		"Tezgahları sil", "Tezgahı, masayı ve ocak üstünü temizle.",
		"Wipe the counters", "Clean the counter, the table and the stovetop."},
	{"kitchen.clean_stove_oven", 1, 30, 14,
		"Ocağı ve fırını temizle", "Yağ ve yanık lekelerini çıkar, ızgaraları yıka.",
		"Clean the stove and oven", "Remove grease and burn marks, wash the grates."},
	{"kitchen.organize_fridge", 1, 20, 14,
		"Buzdolabını düzenle", "Bozulanları at, rafları sil, yiyecekleri düzenle.",
		"Organize the fridge", "Throw out spoiled food, wipe the shelves and organize the food."},
	{"kitchen.clean_microwave", 1, 10, 14,
		"Mikrodalgayı temizle", "İçini ve döner tablayı sil.",
		"Clean the microwave", "Wipe the inside and the turntable."},
	{"bath_laundry.clean_shower_bathtub", 2, 30, 7,
		"Duşu ve küveti temizle", "Kireç ve sabun lekelerini çıkar, camı sil.",
		"Clean the shower and bathtub", "Remove limescale and soap stains, wipe the glass."},
	{"bath_laundry.clean_drains", 2, 20, 14,
		"Giderleri temizle", "Saç ve tıkanıklıkları temizle, gider açıcı kullan.",
		"Clean the drains", "Remove hair and clogs, use drain cleaner."},
	{"bath_laundry.clean_sink_faucet", 2, 10, 3,
		"Lavaboyu ve bataryayı temizle", "Lavaboyu ovala, musluklardaki kireci al.",
		"Clean the sink and faucet", "Scrub the sink and descale the faucets."},
	{"bath_laundry.wipe_mirrors", 2, 10, 7,
		"Aynaları sil", "Aynalardaki su ve diş macunu lekelerini sil.",
		"Wipe the mirrors", "Wipe water and toothpaste stains off the mirrors."},
	{"bath_laundry.change_towels", 2, 10, 7,
		"Havluları değiştir", "Kullanılmış havluları çamaşıra at, temizlerini as.",
		"Change the towels", "Put used towels in the laundry and hang clean ones."},
	{"bath_laundry.mop_bathroom_floor", 2, 20, 7,
		"Banyo zeminini sil", "Zemini paspasla, köşeleri unutma.",
		"Mop the bathroom floor", "Mop the floor, don't forget the corners."},
	{"bath_laundry.organize_bathroom_cabinet", 2, 10, 30,
		"Banyo dolabını düzenle", "Biten ürünleri at, eksikleri not al.",
		"Organize the bathroom cabinet", "Throw out empty products and note what's missing."},
	{"bath_laundry.do_laundry", 2, 20, 3,
		"Çamaşırları yıka", "Renkli ve beyazları ayır, makineyi çalıştır.",
		"Do the laundry", "Separate colors and whites, start the washing machine."},
	{"bath_laundry.hang_laundry", 2, 10, 3,
		"Çamaşırları as", "Makine bitince çamaşırları kurumaya as.",
		"Hang the laundry", "Hang the laundry to dry when the machine is done."},
	{"bath_laundry.fold_laundry", 2, 10, 3,
		"Kuruları topla ve katla", "Kuruyanları katla, herkesin dolabına bırak.",
		"Collect and fold the laundry", "Fold the dry laundry and leave it in everyone's closet."},
	{"bath_laundry.iron_clothes", 2, 20, 7,
		"Ütü yap", "Gömlekleri ve kırışanları ütüle.",
		"Do the ironing", "Iron the shirts and wrinkled clothes."},
	{"bath_laundry.clean_dryer_filter", 2, 10, 7,
		"Kurutma filtresini temizle", "Hav filtresini ve su haznesini boşalt.",
		"Clean the dryer filter", "Empty the lint filter and the water tank."},
	{"toilet.clean_toilet_bowl", 3, 20, 3,
		"Klozeti temizle", "İçini fırçala, kapağını ve oturağını dezenfekte et.",
		"Clean the toilet", "Scrub the bowl, disinfect the lid and seat."},
	{"toilet.clean_toilet_sink", 3, 10, 3,
		"Tuvalet lavabosunu temizle", "Lavaboyu ve musluğu sil.",
		"Clean the toilet sink", "Wipe the sink and the faucet."},
	{"toilet.refill_toilet_paper", 3, 10, 7,
		"Tuvalet kağıdını yenile", "Rulo bitmeden yenisini tak, yedeği kontrol et.",
		"Refill the toilet paper", "Put a new roll on before it runs out, check the spares."},
	{"toilet.empty_toilet_bin", 3, 10, 2,
		"Tuvalet çöpünü boşalt", "Çöpü boşalt ve yeni poşet tak.",
		"Empty the toilet bin", "Empty the bin and put in a new bag."},
	{"toilet.disinfect_floor", 3, 20, 7,
		"Zemini dezenfekte et", "Klozet çevresi dahil zemini sil.",
		"Disinfect the floor", "Wipe the floor, including around the toilet."},
	{"toilet.change_hand_towel", 3, 10, 3,
		"El havlusunu değiştir", "Temiz bir el havlusu as.",
		"Change the hand towel", "Hang a clean hand towel."},
	{"rooms.make_beds", 4, 10, 1,
		"Yatakları topla", "Yorganı düzelt, yastıkları yerleştir.",
		"Make the beds", "Straighten the duvet and arrange the pillows."},
	{"rooms.change_bed_sheets", 4, 20, 14,
		"Nevresimleri değiştir", "Çarşaf ve kılıfları değiştir, kirlileri yıkamaya at.",
		"Change the bed sheets", "Change the sheets and covers, put the dirty ones in the wash."},
	{"rooms.tidy_room", 4, 10, 2,
		"Odayı topla", "Ortada kalanları yerine kaldır.",
		"Tidy the room", "Put away whatever is lying around."},
	{"rooms.tidy_living_room", 4, 10, 1,
		"Salonu topla", "Kanepeyi düzelt, sehpayı boşalt, minderleri yerleştir.",
		"Tidy the living room", "Straighten the sofa, clear the coffee table, arrange the cushions."},
	{"rooms.dust", 4, 10, 7,
		"Toz al", "Raflar, sehpalar ve TV ünitesinin tozunu al.",
		"Dust", "Dust the shelves, coffee tables and TV unit."},
	{"rooms.clean_windows", 4, 20, 30,
		"Camları sil", "Camların içini ve pervazları sil.",
		"Clean the windows", "Wipe the inside of the windows and the sills."},
	{"rooms.organize_wardrobe", 4, 20, 30,
		"Gardırobu düzenle", "Kıyafetleri katla, mevsimlikleri ayır.",
		"Organize the wardrobe", "Fold the clothes and sort out the seasonal ones."},
	{"rooms.check_lamps_sockets", 4, 10, 30,
		"Lambaları ve prizleri kontrol et", "Yanmayan ampulleri değiştir, kabloları düzenle.",
		"Check the lamps and sockets", "Replace burnt-out bulbs and tidy the cables."},
	{"floors_general.vacuum", 5, 20, 3,
		"Süpürge yap", "Tüm odaları ve halıları süpür.",
		"Vacuum", "Vacuum all rooms and rugs."},
	{"floors_general.mop_floors", 5, 20, 7,
		"Paspas yap", "Süpürdükten sonra zeminleri paspasla.",
		"Mop the floors", "Mop the floors after vacuuming."},
	{"floors_general.shake_rugs", 5, 10, 14,
		"Halıları silkele", "Kapı önü ve küçük halıları balkonda silkele.",
		"Shake out the rugs", "Shake out the doormats and small rugs on the balcony."},
	{"floors_general.disinfect_door_handles", 5, 10, 7,
		"Kapı kollarını dezenfekte et", "Kapı kolları, anahtarlar ve kumandaları sil.",
		"Disinfect the door handles", "Wipe the door handles, switches and remote controls."},
	{"floors_general.organize_entryway", 5, 10, 3,
		"Antreyi düzenle", "Ayakkabıları dolaba kaldır, montları as.",
		"Organize the entryway", "Put the shoes in the cabinet and hang up the coats."},
	{"floors_general.deep_clean", 5, 30, 30,
		"Genel temizlik", "Evin tamamında detaylı temizlik.",
		"Deep clean", "A thorough cleaning of the whole house."},
	{"pets.feed_pet", 6, 10, 1,
		"Evcil hayvanı besle", "Mama kabını doldur, porsiyonu kontrol et.",
		"Feed the pet", "Fill the food bowl and check the portion."},
	{"pets.feed_dog", 6, 10, 1,
		"Köpeği besle", "Sabah ve akşam mamasını ver.",
		"Feed the dog", "Give the morning and evening meals."},
	{"pets.feed_cat", 6, 10, 1,
		"Kediyi besle", "Mamasını ver, suyunu tazele.",
		"Feed the cat", "Give the food and refresh the water."},
	{"pets.walk_dog", 6, 20, 1,
		"Köpeği yürüyüşe çıkar", "En az 30 dakikalık bir yürüyüş.",
		"Walk the dog", "A walk of at least 30 minutes."},
	{"pets.clean_litter_box", 6, 10, 1,
		"Kum kabını temizle", "Kumu elekten geçir, gerekirse tamamen değiştir.",
		"Clean the litter box", "Sift the litter, replace it completely if needed."},
	{"pets.refresh_water_bowl", 6, 10, 1,
		"Su kabını tazele", "Kabı yıka ve taze su koy.",
		"Refresh the water bowl", "Wash the bowl and add fresh water."},
	{"pets.brush_pet", 6, 10, 7,
		"Tüylerini tara", "Tüylerini tara, tırnaklarını kontrol et.",
		"Brush the pet", "Brush the fur and check the claws."},
	{"pets.clean_aquarium_cage", 6, 20, 7,
		"Akvaryumu / kafesi temizle", "Suyu değiştir veya kafes altlığını yenile.",
		"Clean the aquarium / cage", "Change the water or replace the cage liner."},
	{"trash_recycling.take_out_trash", 7, 10, 1,
		"Çöpü çıkar", "Tüm çöpleri topla ve konteynere at.",
		"Take out the trash", "Collect all the trash and put it in the container."},
	{"trash_recycling.sort_recycling", 7, 10, 7,
		"Geri dönüşümü ayır", "Plastik, kağıt ve camı ayrı kutulara ayır.",
		"Sort the recycling", "Sort plastic, paper and glass into separate bins."},
	{"trash_recycling.wash_trash_bins", 7, 20, 30,
		"Çöp kovalarını yıka", "Kovaları yıka, kokuyu gider.",
		"Wash the trash bins", "Wash the bins and get rid of the smell."},
	{"trash_recycling.drop_off_batteries_glass", 7, 10, 30,
		"Atık pil ve camı götür", "Biriken atık pil ve camları toplama noktasına götür.",
		"Drop off batteries and glass", "Take the collected batteries and glass to a drop-off point."},
	{"plants_balcony.water_plants", 8, 10, 3,
		"Çiçekleri sula", "Saksıları kontrol et, kuruyanları sula.",
		"Water the plants", "Check the pots and water the dry ones."},
	{"plants_balcony.sweep_balcony", 8, 10, 7,
		"Balkonu süpür", "Balkonu süpür, masa ve sandalyeleri sil.",
		"Sweep the balcony", "Sweep the balcony, wipe the table and chairs."},
	{"plants_balcony.care_for_pots", 8, 10, 30,
		"Saksı bakımı yap", "Kuru yaprakları temizle, gübre ver.",
		"Care for the potted plants", "Remove dry leaves and add fertilizer."},
	{"plants_balcony.tend_garden", 8, 30, 14,
		"Bahçeyi düzenle", "Çimleri biç, yaprakları topla.",
		"Tend the garden", "Mow the lawn and rake the leaves."},
	{"shopping_home.grocery_shopping", 9, 20, 7,
		"Market alışverişi yap", "Ortak listeyi kontrol et ve eksikleri al.",
		"Do the grocery shopping", "Check the shared list and buy what's missing."},
	{"shopping_home.buy_bread_milk", 9, 10, 1,
		"Ekmek ve süt al", "Günlük ekmek ve süt ihtiyacını karşıla.",
		"Buy bread and milk", "Get the daily bread and milk."},
	{"shopping_home.restock_cleaning_supplies", 9, 10, 30,
		"Temizlik malzemelerini tamamla", "Deterjan, sünger ve çöp poşetlerini kontrol et.",
		"Restock cleaning supplies", "Check the detergent, sponges and trash bags."},
	{"shopping_home.pay_bills", 9, 10, 30,
		"Faturaları öde", "Elektrik, su, doğalgaz ve internet faturalarını öde.",
		"Pay the bills", "Pay the electricity, water, gas and internet bills."},
	{"shopping_home.plan_weekly_menu", 9, 20, 7,
		"Haftalık menüyü planla", "Haftanın yemeklerini belirle, listeyi çıkar.",
		"Plan the weekly menu", "Decide the week's meals and make a shopping list."},
}
