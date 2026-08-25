# demo.tape — records docs/demo.gif: a ClickHouse query streaming live
# progress in d9s. Run it from the repository root.
#
# Prerequisites:
#   brew install vhs                       # brings ttyd and ffmpeg with it
#   docker run -d --rm --name d9s-demo-ch -p 19000:9000 \
#     clickhouse/clickhouse-server:24-alpine
#   go build -o d9s ./cmd/d9s
#   cat > /tmp/d9s-demo.yaml <<'EOF'
#   connections:
#     - name: demo-ch
#       type: clickhouse
#       host: 127.0.0.1
#       port: 19000
#       user: default
#       database: default
#       tls:
#         mode: disable
#   EOF
#
# Then:
#   vhs docs/demo.tape
#   docker stop d9s-demo-ch

Output docs/demo.gif

Set FontSize 15
Set Width 1200
Set Height 720
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms

Type "./d9s -config /tmp/d9s-demo.yaml"
Enter
Sleep 1.5s

# Connect to the demo connection and open the default database (second row).
Enter
Sleep 2s
Type "j"
Sleep 500ms
Enter
Sleep 1.5s

# A scan big enough to watch: rows, bytes, percentage and memory climb in the
# status line while it runs.
Type "SELECT number % 10 AS k, count() FROM numbers(8000000000) GROUP BY k"
Sleep 500ms
Ctrl+R
Sleep 14s

# Focus the results and open the profile events the engine reported.
Ctrl+J
Sleep 1s
Type "p"
Sleep 3s
Escape
Sleep 1s

# Leave the interface.
Escape
Sleep 500ms
Escape
Sleep 500ms
Type "q"
Sleep 1s
