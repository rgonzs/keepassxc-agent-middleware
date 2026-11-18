# Controladores de bandeja archivados

Se conserva el código de Windows, macOS y KDE junto con las pruebas, el helper Swift, el script de compilación y las dependencias originales. Este módulo anidado queda fuera de `go build ./...`, `go test ./...` y de los paquetes distribuidos. Es una referencia histórica, no una aplicación independiente lista para compilar.

Los fragmentos del instalador se conservan como contexto de integración. El instalador activo mantiene únicamente compatibilidad para detener y retirar bandejas de versiones anteriores durante una actualización o desinstalación.
