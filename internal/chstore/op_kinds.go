package chstore

// op_kinds.go — v0.10.1391 (operatör: "Prod sağlığı güzel ama VictoriaMetrics
// getirmedi, en azından HTTP metriclerimi getirebilirdi. Onun yerine gRPC
// getirdi.").
//
// CoSRE servis taraması operasyon bulgusunun grafiğini seçerken span TÜRÜNÜ
// bilmiyordu: operation_summary_5m (servis × ad) kind boyutu taşımaz, bu yüzden
// servisin GİDEN gRPC istemci çağrısı (CLIENT) gelen HTTP uç noktasıyla aynı
// sırada yarıştı ve grafiğe o çıktı. Bu okuyucu, YALNIZ grafik adayı birkaç
// operasyon adı için türleri spanmetrics_1m'den (ORDER BY service_name, name,
// kind, … — önek budaması) okur. MV forward-only: kapsam dışı pencere boş
// harita döner (tür bilinmiyor → tercih yok, eski sıra). Ham spans'a ASLA
// düşmez (CLAUDE.md MV-first).

import (
	"context"
	"fmt"
	"time"
)

// opKindsMaxNames — v0.10.1391: tek okumadaki ad tavanı (tarama ≤ 3 grafik
// adayı sorar; tavan yanlış kullanımda IN listesini sınırlar).
const opKindsMaxNames = 16

// opKindsSQL — SAF (v0.10.1391): bind sırası service, names, from, to.
// Zaman-sınırlı WHERE + LIMIT + max_execution_time (CLAUDE.md CH kuralı).
func opKindsSQL(source string) string {
	return fmt.Sprintf(`
		SELECT name, arraySort(groupUniqArray(kind)) AS kinds
		FROM %s
		WHERE service_name = ? AND name IN (?) AND time_bucket >= ? AND time_bucket < ?
		GROUP BY name
		LIMIT %d
		SETTINGS max_execution_time = 3`, source, opKindsMaxNames)
}

// OperationKinds — v0.10.1391: verilen operasyon adlarının pencerede görülen
// span türleri (server | client | producer | consumer | internal …), ad →
// sıralı tür listesi. Boş ad listesi okuma yapmaz.
func (s *Store) OperationKinds(ctx context.Context, service string, names []string, from, to time.Time) (map[string][]string, error) {
	out := map[string][]string{}
	if service == "" || len(names) == 0 {
		return out, nil
	}
	if len(names) > opKindsMaxNames {
		names = names[:opKindsMaxNames]
	}
	rows, err := s.conn.Query(ctx, opKindsSQL(s.spanmetricsSourceFor("spanmetrics_1m")),
		service, names, from.Truncate(time.Minute), to)
	if err != nil {
		return nil, fmt.Errorf("op kinds (spanmetrics_1m): %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var kinds []string
		if err := rows.Scan(&name, &kinds); err != nil {
			return nil, err
		}
		out[name] = kinds
	}
	return out, rows.Err()
}
