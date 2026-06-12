hostek ist ein holistic Server (/code/holistic) der Live-Metriken über den Linux-Server anzeigt.

Zu diesen Metriken zählen Prozesse, und wie diese einzelnd Komponenten beanspruchen (insp. von Windows Task-Manager Default View)

Gleichzeitig sollen aber auch alle technischen Konfiguration des Servers in hostek vorgenommen werden.

hostek implementiert ein Backend zum Verwalten des Linux Servers (Ubuntu) und stellt gleichzeitig ein holistic Front-End zur Verfügung fürs Live-Hardware-Reporting und der User-Konfiguration durch den Admin des Servers.


Admin user (truth = linux user mit sudo rechten) sind die einzigen mit dem Recht, global alle Metriken zu erfassen.

Alle anderen User können nur die gesamt auslastung einzelner hardwarekomponenten sehen, aber keine prozesse die laufen.

Server-Konfiguration kann beispielsweise sein: Der Linux-Server ist immer an (auch wenn kein Monitor angeschlossen ist), (das ist ein klassischen Must-Have für Linux-Server die nur die Rolle des Servers erfüllen), da sie headless sind
