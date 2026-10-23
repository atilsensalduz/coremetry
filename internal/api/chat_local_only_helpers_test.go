package api

import "github.com/cilcenk/coremetry/internal/copilot"

// tickLocalOnly / untickLocalOnly — v0.10.1451: test sunucusunun tüm
// profillerine "Yerel (self-hosted) model" tikini verir / kaldırır (karar 8).
// Sahte uçlar 127.0.0.1'de: beyan doğrulamasından geçer.
func tickLocalOnly(cop *copilot.Service)   { setLocalOnly(cop, true) }
func untickLocalOnly(cop *copilot.Service) { setLocalOnly(cop, false) }

func setLocalOnly(cop *copilot.Service, v bool) {
	ps, def, sm := cop.ProfilesSnapshot()
	for i := range ps {
		ps[i].LocalOnly = v
	}
	cop.SetProfiles(ps, def, sm)
}

// init — copilot'suz test sunucuları (&Server{} + sahte anlatıcı/seçici
// dikişleri, wikiNarrateFn / wikiSelectFn) v0.10.1451 öncesi fikstürlerdir ve
// model çağrısı yapamaz; yerel profil var sayılır. Gerçek copilot taşıyan
// sunucuda dikiş devreye girmez — karar profilin tikinden okunur (yerel profil
// YOKKEN davranışı chat_local_only_test.go gerçek copilot ile pinler).
func init() { localOnlyTestSeam = func() bool { return true } }
