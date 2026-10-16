// Package labels names Frigate's labels for people: an emoji and a name, translated by
// the catalogs. The notifications and the web interface share the table, so a label
// looks the same everywhere.
package labels

import "frigate-telegram-enhanced/internal/i18n"

// Label is how a label is shown. Name is in English, translated by the catalogs,
// except for a brand, shown as it is.
type Label struct {
	Emoji string `json:"emoji"`
	Name  string `json:"name"`
	Brand bool   `json:"-"`
}

// Known holds the labels of Frigate's object model, those of Frigate+, and the sounds
// of its audio detection most used for a home. A label missing here is shown as it is,
// after 🔔 (object) or 🔊 (sound).
var Known = map[string]Label{
	// People and vehicles.
	"person":        {Emoji: "🚶", Name: "Person"},
	"face":          {Emoji: "🙂", Name: "Face"},
	"license_plate": {Emoji: "🔢", Name: "License plate"},
	"bicycle":       {Emoji: "🚲", Name: "Bicycle"},
	"car":           {Emoji: "🚗", Name: "Car"},
	"motorcycle":    {Emoji: "🏍️", Name: "Motorcycle"},
	"bus":           {Emoji: "🚌", Name: "Bus"},
	"truck":         {Emoji: "🚚", Name: "Truck"},
	"train":         {Emoji: "🚆", Name: "Train"},
	"boat":          {Emoji: "🚤", Name: "Boat"},
	"airplane":      {Emoji: "✈️", Name: "Airplane"},
	// Animals.
	"bird":     {Emoji: "🐦", Name: "Bird"},
	"cat":      {Emoji: "🐈", Name: "Cat"},
	"dog":      {Emoji: "🐕", Name: "Dog"},
	"horse":    {Emoji: "🐎", Name: "Horse"},
	"sheep":    {Emoji: "🐑", Name: "Sheep"},
	"cow":      {Emoji: "🐄", Name: "Cow"},
	"goat":     {Emoji: "🐐", Name: "Goat"},
	"bear":     {Emoji: "🐻", Name: "Bear"},
	"deer":     {Emoji: "🦌", Name: "Deer"},
	"fox":      {Emoji: "🦊", Name: "Fox"},
	"rabbit":   {Emoji: "🐇", Name: "Rabbit"},
	"squirrel": {Emoji: "🐿️", Name: "Squirrel"},
	"raccoon":  {Emoji: "🦝", Name: "Raccoon"},
	"skunk":    {Emoji: "🦨", Name: "Skunk"},
	// Things.
	"package":         {Emoji: "📦", Name: "Package"},
	"waste_bin":       {Emoji: "🗑️", Name: "Waste bin"},
	"bbq_grill":       {Emoji: "🍖", Name: "Barbecue"},
	"robot_lawnmower": {Emoji: "🤖", Name: "Robot mower"},
	"umbrella":        {Emoji: "☂️", Name: "Umbrella"},
	"backpack":        {Emoji: "🎒", Name: "Backpack"},
	"handbag":         {Emoji: "👜", Name: "Handbag"},
	"suitcase":        {Emoji: "🧳", Name: "Suitcase"},
	// Delivery companies (Frigate+).
	"amazon":    {Emoji: "🚚", Name: "Amazon", Brand: true},
	"an_post":   {Emoji: "🚚", Name: "An Post", Brand: true},
	"dhl":       {Emoji: "🚚", Name: "DHL", Brand: true},
	"dpd":       {Emoji: "🚚", Name: "DPD", Brand: true},
	"fedex":     {Emoji: "🚚", Name: "FedEx", Brand: true},
	"gls":       {Emoji: "🚚", Name: "GLS", Brand: true},
	"nzpost":    {Emoji: "🚚", Name: "NZ Post", Brand: true},
	"postnl":    {Emoji: "🚚", Name: "PostNL", Brand: true},
	"postnord":  {Emoji: "🚚", Name: "PostNord", Brand: true},
	"purolator": {Emoji: "🚚", Name: "Purolator", Brand: true},
	"ups":       {Emoji: "🚚", Name: "UPS", Brand: true},
	"usps":      {Emoji: "🚚", Name: "USPS", Brand: true},
	// Sounds: voices.
	"speech":           {Emoji: "🗣️", Name: "Speech"},
	"yell":             {Emoji: "📢", Name: "Yelling"},
	"scream":           {Emoji: "😱", Name: "Scream"},
	"whispering":       {Emoji: "🤫", Name: "Whispering"},
	"crying":           {Emoji: "😢", Name: "Crying"},
	"baby_cry":         {Emoji: "👶", Name: "Baby crying"},
	"laughter":         {Emoji: "😂", Name: "Laughter"},
	"singing":          {Emoji: "🎤", Name: "Singing"},
	"whistling":        {Emoji: "😗", Name: "Whistling"},
	"applause":         {Emoji: "👏", Name: "Applause"},
	"cheering":         {Emoji: "🙌", Name: "Cheering"},
	"children_playing": {Emoji: "🧒", Name: "Children playing"},
	"footsteps":        {Emoji: "👣", Name: "Footsteps"},
	// Sounds: animals.
	"bark":     {Emoji: "🐕", Name: "Barking"},
	"howl":     {Emoji: "🐺", Name: "Howling"},
	"growling": {Emoji: "😠", Name: "Growling"},
	"meow":     {Emoji: "🐈", Name: "Meowing"},
	"purr":     {Emoji: "🐈", Name: "Purring"},
	// Sounds: alarms and sirens.
	"fire_alarm":     {Emoji: "🔥", Name: "Fire alarm"},
	"smoke_detector": {Emoji: "🔥", Name: "Smoke alarm"},
	"car_alarm":      {Emoji: "🚨", Name: "Car alarm"},
	"alarm":          {Emoji: "🚨", Name: "Alarm"},
	"alarm_clock":    {Emoji: "⏰", Name: "Alarm clock"},
	"siren":          {Emoji: "🚨", Name: "Siren"},
	"police_car":     {Emoji: "🚓", Name: "Police siren"},
	"ambulance":      {Emoji: "🚑", Name: "Ambulance"},
	"fire_engine":    {Emoji: "🚒", Name: "Fire engine"},
	// Sounds: the house.
	"doorbell":  {Emoji: "🔔", Name: "Doorbell"},
	"ding-dong": {Emoji: "🔔", Name: "Doorbell"},
	"knock":     {Emoji: "🚪", Name: "Knocking"},
	"door":      {Emoji: "🚪", Name: "Door"},
	"glass":     {Emoji: "🪟", Name: "Glass"},
	"shatter":   {Emoji: "💥", Name: "Shattering"},
	"breaking":  {Emoji: "💥", Name: "Breaking"},
	"telephone": {Emoji: "☎️", Name: "Telephone"},
	"ringtone":  {Emoji: "📱", Name: "Ringtone"},
	"music":     {Emoji: "🎵", Name: "Music"},
	// Sounds: outside.
	"vehicle":   {Emoji: "🚙", Name: "Vehicle"},
	"engine":    {Emoji: "⚙️", Name: "Engine"},
	"gunshot":   {Emoji: "🔫", Name: "Gunshot"},
	"explosion": {Emoji: "💥", Name: "Explosion"},
	"fireworks": {Emoji: "🎆", Name: "Fireworks"},
	"thunder":   {Emoji: "⛈️", Name: "Thunder"},
	"rain":      {Emoji: "🌧️", Name: "Rain"},
	"wind":      {Emoji: "🌬️", Name: "Wind"},
}

// Text is the label as shown: its emoji and its name in lang. A label missing from
// Known keeps its name, after 🔊 for a sound and 🔔 for an object; no label at all is
// a detection.
func Text(label string, sound bool, lang i18n.Lang) string {
	if x, ok := Known[label]; ok {
		if x.Brand {
			return x.Emoji + " " + x.Name
		}
		return x.Emoji + " " + lang.T(x.Name)
	}
	if label == "" {
		return "🔔 " + lang.T("Detection")
	}
	if sound {
		return "🔊 " + label
	}
	return "🔔 " + label
}
