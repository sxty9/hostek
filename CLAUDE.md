hostek ist ein Holistic-Service, der Live-Metriken eines Linux-Servers anzeigt und dessen technische Konfiguration verwaltet.

Die Metriken umfassen die laufenden Prozesse und wie diese die einzelnen Hardwarekomponenten beanspruchen.

hostek implementiert ein Backend zur Verwaltung des Linux-Servers (Ubuntu) und stellt zugleich ein Holistic-Frontend für das Live-Hardware-Reporting und die Server-Konfiguration durch den Admin bereit.

Admins (Wahrheitsquelle: Linux-User mit sudo-Rechten) sind die Einzigen mit dem Recht, global alle Metriken samt laufenden Prozessen zu erfassen. Alle übrigen User sehen ausschließlich die Gesamtauslastung einzelner Hardwarekomponenten, nicht die laufenden Prozesse.

Zur technischen Konfiguration zählt die OS-seitige Server-Autonomie: Der Server bleibt dauerhaft eingeschaltet und läuft headless (ohne angeschlossenen Monitor).

<!-- holistic:constitution:begin -->
# Holistic — Verfassung

Für dieses Repository gelten die Holistic-Axiome und Implementierungsregeln.
Ihr verbindlicher Wortlaut wird nicht hier geführt, sondern mit jedem
Implementierungsauftrag mitgeliefert. So gilt immer der aktuelle Stand.

**Arbeitest du im Auftrag von Mercury:** Der Wortlaut steht vollständig in
deinem Prompt. Er hat Vorrang vor jeder anderen Fassung, die dir begegnet.

**Arbeitest du in einer von Hand geöffneten Sitzung:** Implementiere nicht
selbst. Lege die Arbeit als ToDo in Mercury an und führe es aus — dann kommt
der verbindliche Wortlaut auf dem regulären Weg. Der Verfassungs-Bestand wird
in der Laufzeit-Konfiguration der Instanz benannt.
<!-- holistic:constitution:end -->
