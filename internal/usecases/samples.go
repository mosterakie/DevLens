package usecases

// Sample 是一条真实世界的错误日志用例。
//
// 全部取自公开的 GitHub issue，是开发者粘贴的生产环境原始输出，
// 不是编造的示例。保留来源链接以便核对。
type Sample struct {
	Name     string
	Category string
	Source   string
	Title    string
	Log      string
}

// Samples 覆盖 Go、Java、Python、Node.js 以及网络、DNS、数据库等不同环境。
var Samples = []Sample{
	{
		Name:     "01-db-refused",
		Category: "db-refused",
		Source:   "https://github.com/coder/coder/issues/29148",
		Title:    "Coder Pod Restarting Due to Internal Error",
		Log:      "2026-09-08 01:19:09.357 [erro]  pubsub: pubsub disconnected from postgres  error=EOF\n2026-09-08 01:19:09.383 [erro]  pubsub: pubsub failed to dial postgres  network=tcp  address=postgresql.coder-nonprod.svc.cluster.local:5432  timeout_ms=0\n2026-09-08 01:19:09.383 [erro]  pubsub: pubsub failed to connect to postgres  error=\"dial tcp XX.XX.XX.XX:5432: connect: connection refused\"\n2026-09-08 01:19:09.530 [erro]  coderd.chatd.processor: failed to acquire chats  error=\"dial tcp XX.XX.XX.XX:5432: connect: connection refused\"\n2026-09-08 01:19:10.413 [erro]  pubsub: pubsub failed to dial postgres  network=tcp  address=postgresql.coder-nonprod.svc.cluster.local:5432  timeout_ms=0\n2026-09-08 01:19:10.413 [erro]  pubsub: pubsub failed to connect to postgres  error=\"dial tcp XX.XX.XX.XX:5432: connect: connection refused\"\n2026-09-08 01:19:10.504 [erro]  coderd.chatd.processor: failed to acquire chats  error=\"dial tcp XX.XX.XX.XX:5432: connect: connection refused\"\n2026-09-08 01:19:11.511 [erro]  coderd.chatd.processor: failed to acquire chats  error=\"dial tcp XX.XX.XX.XX:5432: connect: connection refused\"\n2026-09-08 01:19:11.511 [warn]  coderd.inmem-provisionerd-coder-649cdcc84d-8tnph-1: heartbeat failed  error=\"dial tcp XX.XX.XX.XX:5432: connect: connection refused\"\n2026-09-08 01:19:11.511 [warn]  coderd.gitsync: acquire stale chat diff statuses  error=\"dial tcp XX.XX.XX.XX:5432: connect: connection refused\"\n2026-09-08 01:19:11.511 [warn]  coderd: run replica update loop ...\n    error= get replicas:\n               github.com/coder/coder/v2/enterprise/replicasync.(*Manager).syncReplicas\n                   /home/runner/work/coder/coder/enterprise/replicasync/replicasync.go:251\n'             - dial tcp XX.XX.XX.XX:5432: connect: connection refused\n2026-09-08 01:19:11.605 [warn]  coderd.inmem-provisionerd-coder-649cdcc84d-8tnph-0: heartbeat failed  error=\"dial tcp XX.XX.XX.XX:5432: connect: connection refused\"",
	},
	{
		Name:     "02-dns",
		Category: "dns",
		Source:   "https://github.com/qdm12/gluetun-servers/issues/12",
		Title:    "Investigate warnings from expressvpn",
		Log:      "2026-08-04T13:00:32Z WARN reached the maximum number of consecutive failures: 4 failed attempts resolving monaco-ca-version-2.expressnetw.com: lookup monaco-ca-version-2.expressnetw.com on 168.63.129.16:53: no such host\n2026-08-04T13:00:32Z WARN reached the maximum number of consecutive failures: 4 failed attempts resolving montenegro-ca-version-2.expressnetw.com: lookup montenegro-ca-version-2.expressnetw.com on 168.63.129.16:53: no such host\n2026-08-04T13:00:32Z WARN reached the maximum number of consecutive failures: 4 failed attempts resolving bosniaandherzegovina-ca-version-2.expressnetw.com: lookup bosniaandherzegovina-ca-version-2.expressnetw.com on 168.63.129.16:53: no such host\n2026-08-04T13:00:32Z WARN reached the maximum number of consecutive failures: 4 failed attempts resolving kenya-ca-version-2.expressnetw.com: lookup kenya-ca-version-2.expressnetw.com on 168.63.129.16:53: no such host\n2026-08-04T13:00:32Z WARN reached the maximum number of consecutive failures: 4 failed attempts resolving malta-ca-version-2.expressnetw.com: lookup malta-ca-version-2.expressnetw.com on 168.63.129.16:53: no such host\n2026-08-04T13:00:32Z WARN reached the maximum number of consecutive failures: 4 failed attempts resolving jersey-ca-version-2.expressnetw.com: lookup jersey-ca-version-2.expressnetw.com on 168.63.129.16:53: no such host\n2026-08-04T13:00:32Z WARN reached the maximum number of consecutive failures: 4 failed attempts resolving uzbekistan-ca-version-2.expressnetw.com: lookup uzbekistan-ca-version-2.expressnetw.com on 168.63.129.16:53: no such host\n2026-08-04T13:00:32Z WARN reached the maximum number of consecutive failures: 4 failed attempts resolving isleofman-ca-version-2.expressnetw.com: lookup isleofman-ca-version-2.expressnetw.com on 168.63.129.16:53: no such host\n2026-08-04T13:00:32Z WARN reached the maximum number of consecutive failures: 4 failed attempts resolving lebanon-ca-version-2.expressnetw.com: lookup lebanon-ca-version-2.expressnetw.com on 168.63.129.16:53: no such host\n2026-08-04T13:00:32Z WARN reached the maximum number of consecutive failures: 4 failed attempts resolving armenia-ca-version-2.expressnetw.com: lookup armenia-ca-version-2.expressnetw.com on 168.63.129.16:53: no such host",
	},
	{
		Name:     "03-go-deadline",
		Category: "go-deadline",
		Source:   "https://github.com/gitmoot/gitmoot/issues/2145",
		Title:    "every repo's poll times out because the org-directive sweep holds the daemon's only SQLite connection: 39 repos frozen at one timestamp, 12 context deadline exceeded",
		Log:      "goroutine 4810 [runnable]:\n  internal/db.(*Store).ListOpenOrgDirectiveObligations        <- HOLDS the connection\n  internal/cli.evaluateOrgDirectiveTTLs        blocked_since.go:482\n\ngoroutine 1 [select]:\n  database/sql.(*DB).conn                      sql.go:1369     <- WAITING for the connection\n  database/sql.(*DB).ExecContext\n  internal/db.(*Store).UpdateRepoPollResult    store_tasks.go:137\n  internal/cli.registeredRepoPoller.pollRepo   daemon_supervision.go:1341\n  internal/cli.pollRegisteredReposWithPoller   daemon_supervision.go:1208",
	},
	{
		Name:     "04-go-deadline",
		Category: "go-deadline",
		Source:   "https://github.com/gastownhall/beads/issues/5769",
		Title:    "Full-package internal/storage/dolt run hits a uniform 45s schema-init timeout wall: orphan test DBs degrade INFORMATION_SCHEMA probes in MigrateUp",
		Log:      "goroutine 37280 [IO wait]:\n  ...mysqlConn.readWithTimeout\n  github.com/steveyegge/beads/internal/storage/schema.columnExists (dep_id_backfill.go:99)\n  ...rekeyAuxRowTable → rekeyAuxRowIDsPending → rekeyAuxRowIDsAllPasses\n  github.com/steveyegge/beads/internal/storage/schema.MigrateUp (schema.go:593)\n  ...initSchemaOnDBWithBootstrapHeal → TestConcurrentInitSchema.func2",
	},
	{
		Name:     "05-java-npe",
		Category: "java-npe",
		Source:   "https://github.com/rsl-mohitgirase/order-service-app/issues/1",
		Title:    "NullPointerException when placing an order with an unknown coupon code",
		Log:      "2026-09-18 16:31:54 [SEVERE] App - Scenario failed: B: order with unknown coupon BLACKFRIDAY\njava.lang.NullPointerException: Cannot invoke \"com.rsl.orderservice.model.Coupon.getPercentOff()\" because \"coupon\" is null\n\tat com.rsl.orderservice.service.DiscountService.discountCents(DiscountService.java:52)\n\tat com.rsl.orderservice.service.OrderService.placeOrder(OrderService.java:78)\n\tat com.rsl.orderservice.App.lambda$main$1(App.java:63)\n\tat com.rsl.orderservice.App.runScenario(App.java:79)\n\tat com.rsl.orderservice.App.main(App.java:62)",
	},
	{
		Name:     "06-java-timeout",
		Category: "java-timeout",
		Source:   "https://github.com/lucas-romanenko/jellyfin-tentacle-androidtv/issues/16",
		Title:    "[auto] app-warn: Tentacle: Caused by: java.net.SocketTimeoutException: failed to connect to /N.N.2.52 (port",
		Log:      "W Tentacle: \t\tat libcore.io.IoBridge.connectErrno(IoBridge.java:235)\nW Tentacle: \t\tat libcore.io.IoBridge.connect(IoBridge.java:179)\nW Tentacle: \t\tat java.net.PlainSocketImpl.socketConnect(PlainSocketImpl.java:142)\nW Tentacle: \t\tat java.net.AbstractPlainSocketImpl.doConnect(AbstractPlainSocketImpl.java:390)\nW Tentacle: \t\tat java.net.AbstractPlainSocketImpl.connectToAddress(AbstractPlainSocketImpl.java:230)\nW Tentacle: \t\tat java.net.AbstractPlainSocketImpl.connect(AbstractPlainSocketImpl.java:212)\nW Tentacle: \t\tat java.net.SocksSocketImpl.connect(SocksSocketImpl.java:436)\nW Tentacle: \t\tat java.net.Socket.connect(Socket.java:646)\nW Tentacle: \t\tat id.e.e(r8-map-id-1549d52b966a4b4553d68558c583f79747f849c21dc92d090a499093ca73e639:4)\nW Tentacle: \t\tat cd.d.i(r8-map-id-1549d52b966a4b4553d68558c583f79747f849c21dc92d090a499093ca73e639:71)\nW Tentacle: \t\tat cd.d.d(r8-map-id-1549d52b966a4b4553d68558c583f79747f849c21dc92d090a499093ca73e639:25)\nW Tentacle: \t\tat cd.j.a(r8-map-id-1549d52b966a4b4553d68558c583f79747f849c21dc92d090a499093ca73e639:3)\nW Tentacle: \t\tat androidx.appcompat.app.m0.a(r8-map-id-1549d52b966a4b4553d68558c583f79747f849c21dc92d090a499093ca73e639:67)\nW Tentacle: \t\tat androidx.appcompat.app.m0.run(r8-map-id-1549d52b966a4b4553d68558c583f79747f849c21dc92d090a499093ca73e639:278)\nW Tentacle: \t\tat java.util.concurrent.ThreadPoolExecutor.runWorker(ThreadPoolExecutor.java:1145)\nW Tentacle: \t\tat java.util.concurrent.ThreadPoolExecutor$Worker.run(ThreadPoolExecutor.java:644)\nW Tentacle: \t\tat java.lang.Thread.run(Thread.java:1012)\nW Tentacle: Caused by: java.net.SocketTimeoutException: failed to connect to /192.168.2.52 (port 8096) from /192.168.2.38 (port 45864) after 6000ms",
	},
	{
		Name:     "07-java-timeout",
		Category: "java-timeout",
		Source:   "https://github.com/Flow-Media-Client/Flow-release/issues/99",
		Title:    "Flow Playback Failure: NETWORK",
		Log:      "cq1: Source error\n\tat yq1.t(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:17)\n\tat yq1.handleMessage(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:465)\n\tat android.os.Handler.dispatchMessage(Handler.java:102)\n\tat android.os.Looper.loopOnce(Looper.java:205)\n\tat android.os.Looper.loop(Looper.java:294)\n\tat android.os.HandlerThread.run(HandlerThread.java:67)\nCaused by: ji2: java.net.SocketTimeoutException: timeout\n\tat o64.read(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:55)\n\tat s21.read(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:6)\n\tat m56.read(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:13)\n\tat w40.read(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:56)\n\tat w40.read(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:56)\n\tat lw5.read(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:3)\n\tat e31.r(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:11)\n\tat e31.a(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:31)\n\tat z54.k(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:13)\n\tat qj3.g(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:77)\n\tat yx4.a(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:267)\n\tat ne3.run(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:35)\n\tat java.util.concurrent.ThreadPoolExecutor.runWorker(ThreadPoolExecutor.java:1145)\n\tat java.util.concurrent.ThreadPoolExecutor$Worker.run(ThreadPoolExecutor.java:644)\n\tat java.lang.Thread.run(Thread.java:1012)\nCaused by: java.net.SocketTimeoutException: timeout\n\tat ei2.l(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:12)\n\tat di2.w(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:200)\n\tat jp1.w(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:14)\n\tat x20.read(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:72)\n\tat o64.read(r8-map-id-b1d3a1d5e0b72c7673183d1c599bd2b2447d08607f4967d757720804b38c93ce:34)\n\t... 14 more",
	},
	{
		Name:     "08-node-econn",
		Category: "node-econn",
		Source:   "https://github.com/YawLabs/oam/issues/164",
		Title:    "net.connect with no 'error' listener reports an unhandled promise rejection, not an uncaught 'error' event",
		Log:      "node: node:events:497\n            throw er; // Unhandled 'error' event\n            ^\n      Error: connect ECONNREFUSED 127.0.0.1:60317\n          at TCPConnectWrap.afterConnect [as oncomplete] (node:net:1637:16)\n      Emitted 'error' event on Socket instance at:\n          at emitErrorNT (node:internal/streams/destroy:170:8)\n          ...                                                              (exit 1)\n\noam:  error[OAM-RT0004]: unhandled promise rejection: Error: connect ECONNREFUSED 127.0.0.1:60319\n          at makeSysError (oam:bootstrap.js:1738:13) { errno: -4078, code: 'ECONNREFUSED', ... }   (exit 1)",
	},
	{
		Name:     "09-panic2",
		Category: "panic2",
		Source:   "https://github.com/hyperledger/fabric-x-committer/issues/827",
		Title:    "[deliverorderer] Nil-pointer panic in ToQueue when it exits before the first config block",
		Log:      "panic: runtime error: invalid memory address or nil pointer dereference\n[signal SIGSEGV: segmentation violation code=0x1 addr=0x10 pc=0xf18566]\n\ngoroutine 133 [running]:\ngithub.com/hyperledger/fabric-x-committer/utils/deliverorderer.ToQueue(...)\n\tutils/deliverorderer/orderer.go:117\ngithub.com/hyperledger/fabric-x-committer/service/sidecar.(*Service).startDelivery(...)\n\tservice/sidecar/sidecar.go:353\ngithub.com/hyperledger/fabric-x-committer/service/sidecar.(*Service).sendBlocksAndReceiveStatus.func2()\n\tservice/sidecar/sidecar.go:220",
	},
	{
		Name:     "10-panic2",
		Category: "panic2",
		Source:   "https://github.com/bjarneo/cliamp/issues/514",
		Title:    "cliamp-crash-normalisation-nil-deref",
		Log:      "panic: runtime error: invalid memory address or nil pointer dereference\n[signal SIGSEGV: segmentation violation code=0x1 addr=0x8 pc=0xd4c3a3]\n\ngoroutine 203 [running]:\ngithub.com/devgianlu/go-librespot/player.calculateNormalisationFactor(...)\n\tgithub.com/devgianlu/go-librespot@v0.9.0/player/player.go:677\ngithub.com/devgianlu/go-librespot/player.(*Player).NewStream(0x1be09a98a50, {0x1782780, 0x1be09132000}, 0x1be09577560, {{0x16be634, 0x5}, {0x1be0968c250, 0x10, 0x10}}, 0x140, ...)\n\tgithub.com/devgianlu/go-librespot@v0.9.0/player/player.go:784 +0x2063\ngithub.com/bjarneo/cliamp/external/spotify.(*Session).NewStream.func1()\n\tgithub.com/bjarneo/cliamp/external/spotify/session.go:609 +0x1de\ngithub.com/bjarneo/cliamp/external/spotify.awaitSpotifyStream.func1()\n\tgithub.com/bjarneo/cliamp/external/spotify/session.go:98 +0x22\ncreated by github.com/bjarneo/cliamp/external/spotify.awaitSpotifyStream in goroutine 387\n\tgithub.com/bjarneo/cliamp/external/spotify/session.go:97 +0xa6",
	},
	{
		Name:     "11-panic2",
		Category: "panic2",
		Source:   "https://github.com/ZhiYi-R/moon-bridge/issues/112",
		Title:    "bug(test): main panics under -tags=e2e in loadDotEnv(nil)",
		Log:      "panic: runtime error: invalid memory address or nil pointer dereference\n[signal SIGSEGV: segmentation violation code=0x1 addr=0x80 pc=...]\n\ngoroutine 1 [running]:\nmoonbridge/internal/e2e_test.loadDotEnv({0x0, 0x0})\n\tinternal/e2e/e2e_test.go:235\nmoonbridge/internal/e2e_test.TestMain(...)\n\tinternal/e2e/e2e_test.go:145",
	},
	{
		Name:     "12-panic2",
		Category: "panic2",
		Source:   "https://github.com/prometheus/node_exporter/issues/3817",
		Title:    "Fibre Channel collector panics when sysfs statistics counters are missing",
		Log:      "node_exporter[1070004]: panic: runtime error: invalid memory address or nil pointer dereference\nnode_exporter[1070004]: [signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0xa2f259]\nnode_exporter[1070004]: goroutine 89 [running]:\nnode_exporter[1070004]: github.com/prometheus/node_exporter/collector.(*fibrechannelCollector).Update(0xc00014cf90, 0xc00037fd50)\nnode_exporter[1070004]:         /Volumes/Home/go/node_exporter/collector/fibrechannel_linux.go:133 +0x479\nnode_exporter[1070004]: github.com/prometheus/node_exporter/collector.execute({0xc098f3, 0xc}, {0xd34e00, 0xc00014cf90}, 0xc00037fd50, 0xc00003e480)\nnode_exporter[1070004]:         /Volumes/Home/go/node_exporter/collector/collector.go:160 +0x82\nnode_exporter[1070004]: github.com/prometheus/node_exporter/collector.NodeCollector.Collect.func1({0xc098f3?, 0x0?}, {0xd34e00?, 0xc00014cf90?})\nnode_exporter[1070004]:         /Volumes/Home/go/node_exporter/collector/collector.go:151 +0x33\nnode_exporter[1070004]: created by github.com/prometheus/node_exporter/collector.NodeCollector.Collect in goroutine 60\nnode_exporter[1070004]:         /Volumes/Home/go/node_exporter/collector/collector.go:150 +0xce",
	},
	{
		Name:     "13-panic2",
		Category: "panic2",
		Source:   "https://github.com/ysya/runscaler/issues/11",
		Title:    "panic: nil pointer dereference in LogFileWriter.Write when log-file = \"\" (v0.9.1)",
		Log:      "panic: runtime error: invalid memory address or nil pointer dereference\n[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x628a4d9]\n\ngoroutine 1 [running]:\ninternal/sync.(*Mutex).Lock(...)\ngithub.com/ysya/runscaler/internal/config.(*LogFileWriter).Write(0x0, {0x21a39bc2600, 0xf1, 0x100})\n        internal/config/logfile.go:49 +0x59\n...\nmain.logServiceDrainTimeoutReminder(...)\n        cmd/runner/main.go:584 +0x1ed\nmain.runManager(...)\n        cmd/runner/main.go:273 +0x43f",
	},
	{
		Name:     "14-panic2",
		Category: "panic2",
		Source:   "https://github.com/databricks/terraform-provider-databricks/issues/5998",
		Title:    "[ISSUE] Exporter: nil pointer panic in emitRfaAccessRequestDestinations when exporting uc-metastores under a narrowed -listing",
		Log:      "[INFO] ... Listing [settings uc-metastores]\n[INFO] Caching groups in memory ...\npanic: runtime error: invalid memory address or nil pointer dereference\n[signal SIGSEGV: segmentation violation code=0x1 addr=0x638 pc=0x190717c]\n\ngoroutine 15 [running]:\ngithub.com/databricks/terraform-provider-databricks/exporter.(*importContext).emitRfaAccessRequestDestinations(...)\n      exporter/impl_uc.go:813 +0x7c\ngithub.com/databricks/terraform-provider-databricks/exporter.importUcMetastores(...)\n      exporter/impl_uc.go:656 +0x8e\ngithub.com/databricks/terraform-provider-databricks/exporter.(*resource).ImportResource.func2()\n      exporter/model.go:412 +0x23\ngithub.com/databricks/terraform-provider-databricks/exporter.runWithRetries[...](...)\n      exporter/util.go:506 +0x1c7\ngithub.com/databricks/terraform-provider-databricks/exporter.(*resource).ImportResource(...)\n      exporter/model.go:411 +0xaa5\ngithub.com/databricks/terraform-provider-databricks/exporter.(*importContext).resourceHandler(...)\n      exporter/context.go:685 +0x28c\ngithub.com/databricks/terraform-provider-databricks/exporter.(*importContext).startImportChannels.func2()\n      exporter/context.go:713 +0x2e\ncreated by ...startImportChannels in goroutine 1\n      exporter/context.go:712 +0x136",
	},
	{
		Name:     "15-py-traceback",
		Category: "py-traceback",
		Source:   "https://github.com/ndahn/HkbEditor/issues/4",
		Title:    "Error when trying to attach or import hierarchy",
		Log:      "[ERROR] 'module' object is not callable\nTraceback (most recent call last):\n  File \"hkb_editor\\gui\\beh_editor.py\", line 1297, in attach_hierarchy\n  File \"hkb_editor\\gui\\workflows\\clone_hierarchy.py\", line 355, in paste_hierarchy\nTypeError: 'module' object is not callable\nTraceback (most recent call last):\n  File \"hkb_editor\\gui\\beh_editor.py\", line 1297, in attach_hierarchy\n  File \"hkb_editor\\gui\\workflows\\clone_hierarchy.py\", line 355, in paste_hierarchy\nTypeError: 'module' object is not callable",
	},
}
