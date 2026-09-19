# metaTask 三池聚合读数 · 逐项对照表（Wish Test #001 · 节点级实现步）

本文件是「MetaSo 侧 metaTask 池聚合」实现步的对照证据：同一份链上材料，三个独立实现
（链上引擎 / 本仓库 Go 实现 / Python 复算脚本）逐项比对。

- 预注册：[pin://3b6ae9e9ec1b7bc9d1bb9ca5f1cd679fae2abe7dab7770ea626f668bc774bdbai0](pin://3b6ae9e9ec1b7bc9d1bb9ca5f1cd679fae2abe7dab7770ea626f668bc774bdbai0)
- 宪章 v0.3（§〇 复算配方 / §三 判据）：[pin://94a3f6a0ccf1edd4ff589daa0c98c420433d2c515b6e1fbed657e67b8bebfcaei0](pin://94a3f6a0ccf1edd4ff589daa0c98c420433d2c515b6e1fbed657e67b8bebfcaei0)（读取路径首通道返回空件，按 §〇.1 走原始索引 + base64 取得：11562 字节，sha256 `54365d20b742605a9e295e050c015f0c8e02144d7793666cf891d8bc61a47f41` = 该 rev 声明的 contentHash）
- 对照引擎：metatask-replay v1.2.0（[pin://de90cb26a480f3132d90b7ff937cc0cfa030bb77edc6fa6826d9b2201c76ff58i0](pin://de90cb26a480f3132d90b7ff937cc0cfa030bb77edc6fa6826d9b2201c76ff58i0)，skill-file metafile://4e34bf7abdf129feec07183bcc742108c175ccb883fc78d056770a8e409c4f00i0，包 sha256 `df9915ae4558523cb514c5098013b11156c2807c77f6837bfb8d3747d7238243`，脚本 sha256 `828a9b95b1f2c9648ca250122cd78063d622202ac53b776508c737a639cf3f25`）

## 一、材料（冻结四件套）

七个 path 各以 `size=50` + cursor 翻页至显式空页；逐 pin 以 `https://manapi.metaid.io/content/<pinId>`
（裸 pinId）取原始字节，全部与列表声明 `contentLength` 逐字节相等（518/518），0 拒收。

| 项 | 值 |
|---|---|
| 抓取时刻 | 2026-09-16 19:31:48–19:32:0x +0800（响应头 Date: Wed, 16 Sep 2026 11:31:48–11:31:51 GMT） |
| 七池条数 | task 2 / tree 2 / spec 23 / claim 117 / release 9 / submission 118 / verify 247 |
| 快照 manifest sha256 | `104474d99cbb0b2c23127bd1a32a328ad2ac6155316650a8e0dcb632c058b2b8` |
| pin 集合 sha256 | `d59aaa30d3b91d1d8a7a90ca72c66b23c262e4160851d656abbccc6fc7cfccdb` |
| 载荷验收 | `ok` 518 / 拒收 0（每页原始响应 sha256 逐页留档于 manifest） |

材料本身**不可自证**：冻结四件套（路径、cutoff 时刻、水位、逐页哈希）随本表一并留档；
池是增长型，任何数字只在标注抓取时刻后成立。

## 二、逐项对照表（试点 #02 任务根，111 节点）

任务根：`08cac496dfa93874dd7d16893038da16b0d2dbc92f844d09512ca0cc78c03b46i0`。
对照字段：每个节点的 `status` / `holder` / `submission` / `effective_pass`，加四个标量
（`quorum` / `all_verified` / `verify_votes` / `reputation`）。合计 **448 个字段逐项一致，差异 0**。

| 对象 | 字段 | metatask-replay v1.2.0 | metaso-p2p internal/metatask | 判定 |
|---|---|---|---|---|
| SCALAR | quorum | 2 | 2 | OK |
| SCALAR | all_verified | False | False | OK |
| SCALAR | verify_votes | 15 | 15 | OK |
| SCALAR | reputation | {'idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm': 1, 'idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7': 3} | {'idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm': 1, 'idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7': 3} | OK |
| 0 | status | open | open | OK |
| 0 | holder | None | None | OK |
| 0 | submission | None | None | OK |
| 0 | effective_pass | 0 | 0 | OK |
| m31 | status | claimed | claimed | OK |
| m31 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| m31 | submission | 5b0debb3015a16c67e299849db215dd5025005d53cdb9814fe8c1f8c5ed97110i0 | 5b0debb3015a16c67e299849db215dd5025005d53cdb9814fe8c1f8c5ed97110i0 | OK |
| m31 | effective_pass | 0 | 0 | OK |
| m41 | status | open | open | OK |
| m41 | holder | None | None | OK |
| m41 | submission | None | None | OK |
| m41 | effective_pass | 0 | 0 | OK |
| m51 | status | claimed | claimed | OK |
| m51 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| m51 | submission | 851ab481238e7b7119c3334efd5d47957624859700035953e13584d2648f930bi0 | 851ab481238e7b7119c3334efd5d47957624859700035953e13584d2648f930bi0 | OK |
| m51 | effective_pass | 0 | 0 | OK |
| m61 | status | claimed | claimed | OK |
| m61 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| m61 | submission | d04303723e7c54ff783d4798439295e4a170818014eaf0fd078a0a2478602b8bi0 | d04303723e7c54ff783d4798439295e4a170818014eaf0fd078a0a2478602b8bi0 | OK |
| m61 | effective_pass | 0 | 0 | OK |
| m71 | status | claimed | claimed | OK |
| m71 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| m71 | submission | 81bdfaae1750fd3cd9666f37734e38c80557412d029ab8ff9041c645a4e94220i0 | 81bdfaae1750fd3cd9666f37734e38c80557412d029ab8ff9041c645a4e94220i0 | OK |
| m71 | effective_pass | 0 | 0 | OK |
| m81 | status | open | open | OK |
| m81 | holder | None | None | OK |
| m81 | submission | None | None | OK |
| m81 | effective_pass | 0 | 0 | OK |
| m91 | status | open | open | OK |
| m91 | holder | None | None | OK |
| m91 | submission | None | None | OK |
| m91 | effective_pass | 0 | 0 | OK |
| t31 | status | claimed | claimed | OK |
| t31 | holder | idq15a3wj5wsddk30vml6jqyetpvlqt2dky3a9869n | idq15a3wj5wsddk30vml6jqyetpvlqt2dky3a9869n | OK |
| t31 | submission | 6d276be9016c0ea30bfba07af86e135729476c05c6d1ed20fc59d831d3425747i0 | 6d276be9016c0ea30bfba07af86e135729476c05c6d1ed20fc59d831d3425747i0 | OK |
| t31 | effective_pass | 0 | 0 | OK |
| t32 | status | claimed | claimed | OK |
| t32 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t32 | submission | 34001f0669a4a5c5725be6f9fad91ec59ea8dd1b143b75659cf5ef6afc37ac0di0 | 34001f0669a4a5c5725be6f9fad91ec59ea8dd1b143b75659cf5ef6afc37ac0di0 | OK |
| t32 | effective_pass | 0 | 0 | OK |
| t33 | status | claimed | claimed | OK |
| t33 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t33 | submission | fbe8fb07faec557dc63718cf85d32c9959a1e78c619328dd87bf88e805e0879ei0 | fbe8fb07faec557dc63718cf85d32c9959a1e78c619328dd87bf88e805e0879ei0 | OK |
| t33 | effective_pass | 0 | 0 | OK |
| t34 | status | claimed | claimed | OK |
| t34 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t34 | submission | 20af5ed2ec655d751c9186aeced8256a04266ff74e0f4992a93f065ee67836a9i0 | 20af5ed2ec655d751c9186aeced8256a04266ff74e0f4992a93f065ee67836a9i0 | OK |
| t34 | effective_pass | 0 | 0 | OK |
| t35 | status | claimed | claimed | OK |
| t35 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t35 | submission | 7ba5c68905232b9d3a044bee019901d05d6a0c7957f27bb2b4bc5d4b1ac6cd02i0 | 7ba5c68905232b9d3a044bee019901d05d6a0c7957f27bb2b4bc5d4b1ac6cd02i0 | OK |
| t35 | effective_pass | 0 | 0 | OK |
| t36 | status | claimed | claimed | OK |
| t36 | holder | idq18x8zm89zrmdf5susdgxtyg4lraf2z5rdejd984 | idq18x8zm89zrmdf5susdgxtyg4lraf2z5rdejd984 | OK |
| t36 | submission | 5ef8b66e75ba93ac581fece5cdb1effad16c54a1b4d6a3b1109558a01afe0275i0 | 5ef8b66e75ba93ac581fece5cdb1effad16c54a1b4d6a3b1109558a01afe0275i0 | OK |
| t36 | effective_pass | 0 | 0 | OK |
| t37 | status | claimed | claimed | OK |
| t37 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t37 | submission | 1b4d0d82ebfa7ba422731a3e06a212d648c169816212d481cef8e3fd4fdd62f9i0 | 1b4d0d82ebfa7ba422731a3e06a212d648c169816212d481cef8e3fd4fdd62f9i0 | OK |
| t37 | effective_pass | 0 | 0 | OK |
| t38 | status | claimed | claimed | OK |
| t38 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t38 | submission | ec29f5e6cad29775329b5ba74498433ee666a52ed2501f0bfdd0eacc2710f59bi0 | ec29f5e6cad29775329b5ba74498433ee666a52ed2501f0bfdd0eacc2710f59bi0 | OK |
| t38 | effective_pass | 0 | 0 | OK |
| t39 | status | claimed | claimed | OK |
| t39 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t39 | submission | 6004d459442c9309016fb32056cac86d44e6a1801de297914f38b6ccbb65be2ci0 | 6004d459442c9309016fb32056cac86d44e6a1801de297914f38b6ccbb65be2ci0 | OK |
| t39 | effective_pass | 0 | 0 | OK |
| t40 | status | claimed | claimed | OK |
| t40 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t40 | submission | d008aa6099d26576c74d3f4fce476c241a327a453f2fddce2375e2210366b694i0 | d008aa6099d26576c74d3f4fce476c241a327a453f2fddce2375e2210366b694i0 | OK |
| t40 | effective_pass | 0 | 0 | OK |
| t41 | status | claimed | claimed | OK |
| t41 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t41 | submission | 062fa076f21ff99594afe6d8f0eba1ddc008498826bb988e83081b820a1691fbi0 | 062fa076f21ff99594afe6d8f0eba1ddc008498826bb988e83081b820a1691fbi0 | OK |
| t41 | effective_pass | 0 | 0 | OK |
| t42 | status | claimed | claimed | OK |
| t42 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t42 | submission | 960e91dc30b18703d1df650e50abd0558d7a5f909ac8018dfa65638e170006f1i0 | 960e91dc30b18703d1df650e50abd0558d7a5f909ac8018dfa65638e170006f1i0 | OK |
| t42 | effective_pass | 0 | 0 | OK |
| t43 | status | claimed | claimed | OK |
| t43 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t43 | submission | e91a131ca04e357ec6d4a7fd2cec72136c7c7d7207afcc6a0b13ab3e9a8ad123i0 | e91a131ca04e357ec6d4a7fd2cec72136c7c7d7207afcc6a0b13ab3e9a8ad123i0 | OK |
| t43 | effective_pass | 0 | 0 | OK |
| t44 | status | claimed | claimed | OK |
| t44 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t44 | submission | 79e9837eab2b864d2bb9feba689fbf901275a795098953a4170e53386168a020i0 | 79e9837eab2b864d2bb9feba689fbf901275a795098953a4170e53386168a020i0 | OK |
| t44 | effective_pass | 0 | 0 | OK |
| t45 | status | claimed | claimed | OK |
| t45 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t45 | submission | 37cc8385c1ac66c2e9301bb3bb10880a42fdb9dfec95ed9afde285ce6722eedai0 | 37cc8385c1ac66c2e9301bb3bb10880a42fdb9dfec95ed9afde285ce6722eedai0 | OK |
| t45 | effective_pass | 0 | 0 | OK |
| t46 | status | claimed | claimed | OK |
| t46 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t46 | submission | 0b33ae980c2318890e3e03dc07b51f1092266a2127cfafd32d31dee9a6e80b3ci0 | 0b33ae980c2318890e3e03dc07b51f1092266a2127cfafd32d31dee9a6e80b3ci0 | OK |
| t46 | effective_pass | 0 | 0 | OK |
| t47 | status | claimed | claimed | OK |
| t47 | holder | idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5 | idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5 | OK |
| t47 | submission | b0d1a9d3995d7c4294c8b780f6f367b86c09ef5a8d4ae6aafd3c7494e21710f0i0 | b0d1a9d3995d7c4294c8b780f6f367b86c09ef5a8d4ae6aafd3c7494e21710f0i0 | OK |
| t47 | effective_pass | 0 | 0 | OK |
| t48 | status | claimed | claimed | OK |
| t48 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t48 | submission | 79a7e21b509c25c86fa9339d20cd8a9bfe6221017bd5449333c16fd18f8b68c9i0 | 79a7e21b509c25c86fa9339d20cd8a9bfe6221017bd5449333c16fd18f8b68c9i0 | OK |
| t48 | effective_pass | 0 | 0 | OK |
| t49 | status | claimed | claimed | OK |
| t49 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t49 | submission | c1e3ccb3a572b56ab946079c05c1d0da5e087a74f1ca744ea8136a0b1118949di0 | c1e3ccb3a572b56ab946079c05c1d0da5e087a74f1ca744ea8136a0b1118949di0 | OK |
| t49 | effective_pass | 0 | 0 | OK |
| t50 | status | claimed | claimed | OK |
| t50 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t50 | submission | 2d2b3e20f5eca0b6595985baefd5cfbe576bf08d07e65f048e875678d9e14364i0 | 2d2b3e20f5eca0b6595985baefd5cfbe576bf08d07e65f048e875678d9e14364i0 | OK |
| t50 | effective_pass | 0 | 0 | OK |
| t51 | status | claimed | claimed | OK |
| t51 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t51 | submission | c42d4318581156d08f7fe3350809a43fa641ece0f2562df12c4fc87aeb798692i0 | c42d4318581156d08f7fe3350809a43fa641ece0f2562df12c4fc87aeb798692i0 | OK |
| t51 | effective_pass | 0 | 0 | OK |
| t52 | status | claimed | claimed | OK |
| t52 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t52 | submission | bf8e1727c1496b417a3c5231169672fae8dc832d96b7738fe5f9f9d4eb002a3fi0 | bf8e1727c1496b417a3c5231169672fae8dc832d96b7738fe5f9f9d4eb002a3fi0 | OK |
| t52 | effective_pass | 0 | 0 | OK |
| t53 | status | claimed | claimed | OK |
| t53 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t53 | submission | ab49f34eb02af4895f1a6733b365e2d2e03e49ccde03c830b8b3f4a9a50a129ai0 | ab49f34eb02af4895f1a6733b365e2d2e03e49ccde03c830b8b3f4a9a50a129ai0 | OK |
| t53 | effective_pass | 0 | 0 | OK |
| t54 | status | claimed | claimed | OK |
| t54 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t54 | submission | 96e867f606bfe5d9fa31fe4441449ecaf14a4c535646c43c10e347d4d360fb7di0 | 96e867f606bfe5d9fa31fe4441449ecaf14a4c535646c43c10e347d4d360fb7di0 | OK |
| t54 | effective_pass | 0 | 0 | OK |
| t55 | status | verified | verified | OK |
| t55 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t55 | submission | c84ebd776aac1b4030dc07c7e205b082a8e3eaaac95c5929968321c0993d95c8i0 | c84ebd776aac1b4030dc07c7e205b082a8e3eaaac95c5929968321c0993d95c8i0 | OK |
| t55 | effective_pass | 2 | 2 | OK |
| t56 | status | claimed | claimed | OK |
| t56 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t56 | submission | 3c9d7633bf9a568e392f99e1772168b0e5202c02a3d58f7e35aa9c62f9cbc867i0 | 3c9d7633bf9a568e392f99e1772168b0e5202c02a3d58f7e35aa9c62f9cbc867i0 | OK |
| t56 | effective_pass | 0 | 0 | OK |
| t57 | status | claimed | claimed | OK |
| t57 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t57 | submission | 473c5a1022538c4b99a0f3537ad6de8ca0615ddb96ec30885f0e20d35d5bfa67i0 | 473c5a1022538c4b99a0f3537ad6de8ca0615ddb96ec30885f0e20d35d5bfa67i0 | OK |
| t57 | effective_pass | 0 | 0 | OK |
| t58 | status | claimed | claimed | OK |
| t58 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t58 | submission | 57e88712ca0fb9e616ff2438c4e249c64557a028ea157417f34df905d4f937a8i0 | 57e88712ca0fb9e616ff2438c4e249c64557a028ea157417f34df905d4f937a8i0 | OK |
| t58 | effective_pass | 0 | 0 | OK |
| t59 | status | claimed | claimed | OK |
| t59 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t59 | submission | 5f268797eef345876724d601123887f136624808c58ac0d8e943366801511b9ai0 | 5f268797eef345876724d601123887f136624808c58ac0d8e943366801511b9ai0 | OK |
| t59 | effective_pass | 0 | 0 | OK |
| t60 | status | claimed | claimed | OK |
| t60 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t60 | submission | 47d08b87c18b02b76fde84d5770341dcb8877d6dbb195cfdcd8ef956e5e916eei0 | 47d08b87c18b02b76fde84d5770341dcb8877d6dbb195cfdcd8ef956e5e916eei0 | OK |
| t60 | effective_pass | 0 | 0 | OK |
| t61 | status | claimed | claimed | OK |
| t61 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t61 | submission | 6696c92f942b66cd537a03f9b8ceeda5f634726a4375ff76dcc474d64c56c5f6i0 | 6696c92f942b66cd537a03f9b8ceeda5f634726a4375ff76dcc474d64c56c5f6i0 | OK |
| t61 | effective_pass | 0 | 0 | OK |
| t62 | status | claimed | claimed | OK |
| t62 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t62 | submission | aa6b474fec5da8aac0912dd5e4041de38ce646124b1bab6ad88fb756b976b13bi0 | aa6b474fec5da8aac0912dd5e4041de38ce646124b1bab6ad88fb756b976b13bi0 | OK |
| t62 | effective_pass | 0 | 0 | OK |
| t63 | status | claimed | claimed | OK |
| t63 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t63 | submission | fce285bff772e1bbb060b206581e25b39c5931ec362d2ef1f7404cf48de40ed7i0 | fce285bff772e1bbb060b206581e25b39c5931ec362d2ef1f7404cf48de40ed7i0 | OK |
| t63 | effective_pass | 0 | 0 | OK |
| t64 | status | claimed | claimed | OK |
| t64 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t64 | submission | 2906b6f6a0c64fd29aefb5a9325a598d601ed8d7a01572228512ed62261f4ae3i0 | 2906b6f6a0c64fd29aefb5a9325a598d601ed8d7a01572228512ed62261f4ae3i0 | OK |
| t64 | effective_pass | 0 | 0 | OK |
| t65 | status | claimed | claimed | OK |
| t65 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t65 | submission | 457e3d0045d57de0e92b139db95a90942926bd507f88952fdbc3c03afad2823bi0 | 457e3d0045d57de0e92b139db95a90942926bd507f88952fdbc3c03afad2823bi0 | OK |
| t65 | effective_pass | 0 | 0 | OK |
| t66 | status | claimed | claimed | OK |
| t66 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t66 | submission | f8673f70fe98dde7c85e0572925cbf496623479022bc7077faa63ffdf3465146i0 | f8673f70fe98dde7c85e0572925cbf496623479022bc7077faa63ffdf3465146i0 | OK |
| t66 | effective_pass | 0 | 0 | OK |
| t67 | status | claimed | claimed | OK |
| t67 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t67 | submission | 7ca64873dd31989f7720dcbad886ea46f835dca1fb27c94cca074307d5022cb0i0 | 7ca64873dd31989f7720dcbad886ea46f835dca1fb27c94cca074307d5022cb0i0 | OK |
| t67 | effective_pass | 0 | 0 | OK |
| t68 | status | claimed | claimed | OK |
| t68 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t68 | submission | 63513439838c3a017301f7fbdf206432864cfff7e80859431b7c8efb937ae247i0 | 63513439838c3a017301f7fbdf206432864cfff7e80859431b7c8efb937ae247i0 | OK |
| t68 | effective_pass | 0 | 0 | OK |
| t69 | status | claimed | claimed | OK |
| t69 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t69 | submission | 4c698efdf8a02cf300ff4fd87ace6f8642e066b34893d4ed577a9b4a341f16b1i0 | 4c698efdf8a02cf300ff4fd87ace6f8642e066b34893d4ed577a9b4a341f16b1i0 | OK |
| t69 | effective_pass | 0 | 0 | OK |
| t70 | status | claimed | claimed | OK |
| t70 | holder | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm | OK |
| t70 | submission | e2e2a4d89d5695e42856c71f69fdd8cdaff2ab8130b72dbbc29f0ac58b753d7ei0 | e2e2a4d89d5695e42856c71f69fdd8cdaff2ab8130b72dbbc29f0ac58b753d7ei0 | OK |
| t70 | effective_pass | 0 | 0 | OK |
| t71 | status | claimed | claimed | OK |
| t71 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t71 | submission | c1c74b60ff104b678749eb41e2c96acc419d5a424711364a064639238c3a2a8di0 | c1c74b60ff104b678749eb41e2c96acc419d5a424711364a064639238c3a2a8di0 | OK |
| t71 | effective_pass | 0 | 0 | OK |
| t72 | status | claimed | claimed | OK |
| t72 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t72 | submission | d81f50321774ebc90e67933abad20307786f1d53efd757845f9cb23395659565i0 | d81f50321774ebc90e67933abad20307786f1d53efd757845f9cb23395659565i0 | OK |
| t72 | effective_pass | 0 | 0 | OK |
| t73 | status | claimed | claimed | OK |
| t73 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t73 | submission | 5abb8b17f1e943c7dcae9b6b5d53d3ebb434bfb5be50dbcee6b0a444858f0f6di0 | 5abb8b17f1e943c7dcae9b6b5d53d3ebb434bfb5be50dbcee6b0a444858f0f6di0 | OK |
| t73 | effective_pass | 0 | 0 | OK |
| t74 | status | claimed | claimed | OK |
| t74 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t74 | submission | f23799416e67ac5fca83d0c079467e453e5273999db16508192acb5619ba4c85i0 | f23799416e67ac5fca83d0c079467e453e5273999db16508192acb5619ba4c85i0 | OK |
| t74 | effective_pass | 0 | 0 | OK |
| t75 | status | verified | verified | OK |
| t75 | holder | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | OK |
| t75 | submission | 19d96f36349f610eb5bea0508e1d8b03ae2eeba4aae280866d7322fc38b8fb8ai0 | 19d96f36349f610eb5bea0508e1d8b03ae2eeba4aae280866d7322fc38b8fb8ai0 | OK |
| t75 | effective_pass | 2 | 2 | OK |
| t76 | status | claimed | claimed | OK |
| t76 | holder | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | OK |
| t76 | submission | 4d5a9852bc06748ccb846525e2ecdf0bb1e3646e37ca444c42c1cdf02614d134i0 | 4d5a9852bc06748ccb846525e2ecdf0bb1e3646e37ca444c42c1cdf02614d134i0 | OK |
| t76 | effective_pass | 0 | 0 | OK |
| t77 | status | verified | verified | OK |
| t77 | holder | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | OK |
| t77 | submission | 28c3177a1e500dc719949284d54aa9e269247fe062ca2949518fd407171270f4i0 | 28c3177a1e500dc719949284d54aa9e269247fe062ca2949518fd407171270f4i0 | OK |
| t77 | effective_pass | 2 | 2 | OK |
| t78 | status | claimed | claimed | OK |
| t78 | holder | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | OK |
| t78 | submission | 29a4c16035487f62fa8d1f688307a8d6ca05d853818f8089b9cfb7b33382c3abi0 | 29a4c16035487f62fa8d1f688307a8d6ca05d853818f8089b9cfb7b33382c3abi0 | OK |
| t78 | effective_pass | 0 | 0 | OK |
| t79 | status | verified | verified | OK |
| t79 | holder | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | OK |
| t79 | submission | 1ed6330ba6730ad5726af61f44c399de2713302fa2c39a33cd6cbc4f2bbb837ci0 | 1ed6330ba6730ad5726af61f44c399de2713302fa2c39a33cd6cbc4f2bbb837ci0 | OK |
| t79 | effective_pass | 2 | 2 | OK |
| t80 | status | claimed | claimed | OK |
| t80 | holder | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | OK |
| t80 | submission | e0bc10a3de6c4979694a90b0f71ffd1ba5de510c35f764708c90e4380e2bd7fci0 | e0bc10a3de6c4979694a90b0f71ffd1ba5de510c35f764708c90e4380e2bd7fci0 | OK |
| t80 | effective_pass | 0 | 0 | OK |
| t81 | status | open | open | OK |
| t81 | holder | None | None | OK |
| t81 | submission | None | None | OK |
| t81 | effective_pass | 0 | 0 | OK |
| t82 | status | open | open | OK |
| t82 | holder | None | None | OK |
| t82 | submission | None | None | OK |
| t82 | effective_pass | 0 | 0 | OK |
| t83 | status | open | open | OK |
| t83 | holder | None | None | OK |
| t83 | submission | None | None | OK |
| t83 | effective_pass | 0 | 0 | OK |
| t84 | status | open | open | OK |
| t84 | holder | None | None | OK |
| t84 | submission | None | None | OK |
| t84 | effective_pass | 0 | 0 | OK |
| t85 | status | open | open | OK |
| t85 | holder | None | None | OK |
| t85 | submission | None | None | OK |
| t85 | effective_pass | 0 | 0 | OK |
| t86 | status | open | open | OK |
| t86 | holder | None | None | OK |
| t86 | submission | None | None | OK |
| t86 | effective_pass | 0 | 0 | OK |
| t87 | status | open | open | OK |
| t87 | holder | None | None | OK |
| t87 | submission | None | None | OK |
| t87 | effective_pass | 0 | 0 | OK |
| t88 | status | open | open | OK |
| t88 | holder | None | None | OK |
| t88 | submission | None | None | OK |
| t88 | effective_pass | 0 | 0 | OK |
| t89 | status | open | open | OK |
| t89 | holder | None | None | OK |
| t89 | submission | None | None | OK |
| t89 | effective_pass | 0 | 0 | OK |
| t90 | status | open | open | OK |
| t90 | holder | None | None | OK |
| t90 | submission | None | None | OK |
| t90 | effective_pass | 0 | 0 | OK |
| t91 | status | open | open | OK |
| t91 | holder | None | None | OK |
| t91 | submission | None | None | OK |
| t91 | effective_pass | 0 | 0 | OK |
| t92 | status | open | open | OK |
| t92 | holder | None | None | OK |
| t92 | submission | None | None | OK |
| t92 | effective_pass | 0 | 0 | OK |
| t93 | status | open | open | OK |
| t93 | holder | None | None | OK |
| t93 | submission | None | None | OK |
| t93 | effective_pass | 0 | 0 | OK |
| t94 | status | open | open | OK |
| t94 | holder | None | None | OK |
| t94 | submission | None | None | OK |
| t94 | effective_pass | 0 | 0 | OK |
| t95 | status | open | open | OK |
| t95 | holder | None | None | OK |
| t95 | submission | None | None | OK |
| t95 | effective_pass | 0 | 0 | OK |
| t96 | status | open | open | OK |
| t96 | holder | None | None | OK |
| t96 | submission | None | None | OK |
| t96 | effective_pass | 0 | 0 | OK |
| t97 | status | open | open | OK |
| t97 | holder | None | None | OK |
| t97 | submission | None | None | OK |
| t97 | effective_pass | 0 | 0 | OK |
| t98 | status | open | open | OK |
| t98 | holder | None | None | OK |
| t98 | submission | None | None | OK |
| t98 | effective_pass | 0 | 0 | OK |
| t99 | status | open | open | OK |
| t99 | holder | None | None | OK |
| t99 | submission | None | None | OK |
| t99 | effective_pass | 0 | 0 | OK |
| m101 | status | open | open | OK |
| m101 | holder | None | None | OK |
| m101 | submission | None | None | OK |
| m101 | effective_pass | 0 | 0 | OK |
| m111 | status | open | open | OK |
| m111 | holder | None | None | OK |
| m111 | submission | None | None | OK |
| m111 | effective_pass | 0 | 0 | OK |
| m121 | status | open | open | OK |
| m121 | holder | None | None | OK |
| m121 | submission | None | None | OK |
| m121 | effective_pass | 0 | 0 | OK |
| t100 | status | open | open | OK |
| t100 | holder | None | None | OK |
| t100 | submission | None | None | OK |
| t100 | effective_pass | 0 | 0 | OK |
| t101 | status | open | open | OK |
| t101 | holder | None | None | OK |
| t101 | submission | None | None | OK |
| t101 | effective_pass | 0 | 0 | OK |
| t102 | status | claimed | claimed | OK |
| t102 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t102 | submission | f4ba64ff76568a4edcaf6a0f6babc272b186adaa448f7b268e6f68f1a2116ae1i0 | f4ba64ff76568a4edcaf6a0f6babc272b186adaa448f7b268e6f68f1a2116ae1i0 | OK |
| t102 | effective_pass | 0 | 0 | OK |
| t103 | status | open | open | OK |
| t103 | holder | None | None | OK |
| t103 | submission | None | None | OK |
| t103 | effective_pass | 0 | 0 | OK |
| t104 | status | open | open | OK |
| t104 | holder | None | None | OK |
| t104 | submission | None | None | OK |
| t104 | effective_pass | 0 | 0 | OK |
| t105 | status | open | open | OK |
| t105 | holder | None | None | OK |
| t105 | submission | None | None | OK |
| t105 | effective_pass | 0 | 0 | OK |
| t106 | status | open | open | OK |
| t106 | holder | None | None | OK |
| t106 | submission | None | None | OK |
| t106 | effective_pass | 0 | 0 | OK |
| t107 | status | claimed | claimed | OK |
| t107 | holder | idq1syz3ctftpyfjsr3chew8ae5d0xv7ts2m0rw723 | idq1syz3ctftpyfjsr3chew8ae5d0xv7ts2m0rw723 | OK |
| t107 | submission | f453eec275956c535ffa3a93dfcdb80e2c3079a09e1f569c59164ae57359138ci0 | f453eec275956c535ffa3a93dfcdb80e2c3079a09e1f569c59164ae57359138ci0 | OK |
| t107 | effective_pass | 0 | 0 | OK |
| t108 | status | claimed | claimed | OK |
| t108 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t108 | submission | b6c25868c13dbf7788c7b42b0fcfe12d00a4cac23ad191fe38801234755a1f79i0 | b6c25868c13dbf7788c7b42b0fcfe12d00a4cac23ad191fe38801234755a1f79i0 | OK |
| t108 | effective_pass | 0 | 0 | OK |
| t109 | status | open | open | OK |
| t109 | holder | None | None | OK |
| t109 | submission | None | None | OK |
| t109 | effective_pass | 0 | 0 | OK |
| t110 | status | open | open | OK |
| t110 | holder | None | None | OK |
| t110 | submission | None | None | OK |
| t110 | effective_pass | 0 | 0 | OK |
| t111 | status | claimed | claimed | OK |
| t111 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t111 | submission | da16b215b1a8858c5d3b2da57e8324a4689fe43cd5a28e85d8fb6b8fcacf4baci0 | da16b215b1a8858c5d3b2da57e8324a4689fe43cd5a28e85d8fb6b8fcacf4baci0 | OK |
| t111 | effective_pass | 0 | 0 | OK |
| t112 | status | claimed | claimed | OK |
| t112 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t112 | submission | 998a6be2d809513ce5bd586545e9f1e6c993f22b0d8b4e78eb95eba1e320b8e4i0 | 998a6be2d809513ce5bd586545e9f1e6c993f22b0d8b4e78eb95eba1e320b8e4i0 | OK |
| t112 | effective_pass | 0 | 0 | OK |
| t113 | status | claimed | claimed | OK |
| t113 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t113 | submission | dd4fd6633ac00c8ed0b734bd64cf16502aa919e181642f8ed759a7030da5962ci0 | dd4fd6633ac00c8ed0b734bd64cf16502aa919e181642f8ed759a7030da5962ci0 | OK |
| t113 | effective_pass | 0 | 0 | OK |
| t114 | status | open | open | OK |
| t114 | holder | None | None | OK |
| t114 | submission | None | None | OK |
| t114 | effective_pass | 0 | 0 | OK |
| t115 | status | open | open | OK |
| t115 | holder | None | None | OK |
| t115 | submission | None | None | OK |
| t115 | effective_pass | 0 | 0 | OK |
| t116 | status | claimed | claimed | OK |
| t116 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t116 | submission | d75fae3f947569a213c5f4181f6955e158ddc7d7fd102c7ea63c025ebfe6542ci0 | d75fae3f947569a213c5f4181f6955e158ddc7d7fd102c7ea63c025ebfe6542ci0 | OK |
| t116 | effective_pass | 0 | 0 | OK |
| t117 | status | claimed | claimed | OK |
| t117 | holder | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7 | OK |
| t117 | submission | 1c6433a0a3ce1a63c0101ce5c2ff4b52ff0b5fd2ffd8b6c6a4e4287af5b4c839i0 | 1c6433a0a3ce1a63c0101ce5c2ff4b52ff0b5fd2ffd8b6c6a4e4287af5b4c839i0 | OK |
| t117 | effective_pass | 0 | 0 | OK |
| t118 | status | open | open | OK |
| t118 | holder | None | None | OK |
| t118 | submission | None | None | OK |
| t118 | effective_pass | 0 | 0 | OK |
| t119 | status | claimed | claimed | OK |
| t119 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t119 | submission | 71b0da10cf54df6cfac69b2c537de21269ab3b17e5b0a9d8a89da58a9896979ai0 | 71b0da10cf54df6cfac69b2c537de21269ab3b17e5b0a9d8a89da58a9896979ai0 | OK |
| t119 | effective_pass | 0 | 0 | OK |
| t120 | status | claimed | claimed | OK |
| t120 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t120 | submission | 2af71cc3e75d332b09026e20caf35e5f0a904e5bcaf36698ecf135c6b5eefca1i0 | 2af71cc3e75d332b09026e20caf35e5f0a904e5bcaf36698ecf135c6b5eefca1i0 | OK |
| t120 | effective_pass | 0 | 0 | OK |
| t121 | status | claimed | claimed | OK |
| t121 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t121 | submission | e9c461e1b710cafa0dce4e6368f35e3ce9549d8a00089e3382c3663ebbc878f6i0 | e9c461e1b710cafa0dce4e6368f35e3ce9549d8a00089e3382c3663ebbc878f6i0 | OK |
| t121 | effective_pass | 0 | 0 | OK |
| t122 | status | claimed | claimed | OK |
| t122 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t122 | submission | 73f4d0d0ae05d8699c3fbf37ae5236ca4bc5900bd9b75338ac9966f3b4f7cd36i0 | 73f4d0d0ae05d8699c3fbf37ae5236ca4bc5900bd9b75338ac9966f3b4f7cd36i0 | OK |
| t122 | effective_pass | 0 | 0 | OK |
| t123 | status | claimed | claimed | OK |
| t123 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t123 | submission | c23b2ed5298390315984b052656dbb24af891ada9ec8de157b5ddd14c5faad33i0 | c23b2ed5298390315984b052656dbb24af891ada9ec8de157b5ddd14c5faad33i0 | OK |
| t123 | effective_pass | 0 | 0 | OK |
| t124 | status | claimed | claimed | OK |
| t124 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t124 | submission | d1ffd837a873b9807c3efe1f4b8e0da0a246eb85fc5bbca53ac6dff6b2120be9i0 | d1ffd837a873b9807c3efe1f4b8e0da0a246eb85fc5bbca53ac6dff6b2120be9i0 | OK |
| t124 | effective_pass | 0 | 0 | OK |
| t125 | status | claimed | claimed | OK |
| t125 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t125 | submission | 5ae35fd894d5ba5b9066a24b2a0caf6f73c827c4ed0dcc162cd3dcc7f3e38b7di0 | 5ae35fd894d5ba5b9066a24b2a0caf6f73c827c4ed0dcc162cd3dcc7f3e38b7di0 | OK |
| t125 | effective_pass | 0 | 0 | OK |
| t126 | status | claimed | claimed | OK |
| t126 | holder | idq1syz3ctftpyfjsr3chew8ae5d0xv7ts2m0rw723 | idq1syz3ctftpyfjsr3chew8ae5d0xv7ts2m0rw723 | OK |
| t126 | submission | ef7759924df59182cdf9c9fe44730a75849f525b8408d9a4f3ca4608292b4d1bi0 | ef7759924df59182cdf9c9fe44730a75849f525b8408d9a4f3ca4608292b4d1bi0 | OK |
| t126 | effective_pass | 0 | 0 | OK |
| t127 | status | claimed | claimed | OK |
| t127 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t127 | submission | 964ff438d6ba67a8548e56cbb0b9bbdcc4b7b480b43c11f5cf38db7babc00c8ei0 | 964ff438d6ba67a8548e56cbb0b9bbdcc4b7b480b43c11f5cf38db7babc00c8ei0 | OK |
| t127 | effective_pass | 0 | 0 | OK |
| t128 | status | claimed | claimed | OK |
| t128 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t128 | submission | adeded61568a05aee3bbcc441f6d689a4573431edc9150970e7a9522994179bei0 | adeded61568a05aee3bbcc441f6d689a4573431edc9150970e7a9522994179bei0 | OK |
| t128 | effective_pass | 0 | 0 | OK |
| t129 | status | claimed | claimed | OK |
| t129 | holder | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | idq16c6vlfx70cgmfy9ye2cpgqz2at9p9ckfevugyz | OK |
| t129 | submission | 4217b3ce51172163321f106c596db82305c7e64cb1c9c374c10e974e850a7529i0 | 4217b3ce51172163321f106c596db82305c7e64cb1c9c374c10e974e850a7529i0 | OK |
| t129 | effective_pass | 0 | 0 | OK |
| t130 | status | claimed | claimed | OK |
| t130 | holder | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | idq16tna2gq3tq3yn67pkrl2te46necnhw7zvqfetr | OK |
| t130 | submission | b70f122b16a510b1a084c01b53941e4a4ef95a07f79ae69ffa23e0870f9f811bi0 | b70f122b16a510b1a084c01b53941e4a4ef95a07f79ae69ffa23e0870f9f811bi0 | OK |
| t130 | effective_pass | 0 | 0 | OK |

### 三方一致性（同一份材料）

| 对照 | 节点数 | 对照字段 | 差异 |
|---|---|---|---|
| 引擎 v1.2.0 ↔ 本仓库 Go 实现 | 111 | 448 | **0** |
| 引擎 v1.2.0 ↔ Python 复算脚本 | 111 | 448 | **0** |
| Go 实现 ↔ Python 复算脚本 | 111 | 448 | **0** |

标量（三者一致）：`quorum=2`、`all_verified=false`、`verify_votes=15`、`reputation={idq1g35d5yftpq3jv0ukejte7z76qdqp7sve8l2etm:1, idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7:3}`。

## 三、逐项对照表（历史任务根 · 试点 #01，5 节点）

任务根：`9eb9878732ab85336956a724138184200593bab3f1181aa42322ae83138f481di0`。
对照字段同上；**24 个字段逐项一致，差异 0**。

| 对象 | 字段 | metatask-replay v1.2.0 | metaso-p2p internal/metatask | 判定 |
|---|---|---|---|---|
| SCALAR | quorum | 2 | 2 | OK |
| SCALAR | all_verified | False | False | OK |
| SCALAR | verify_votes | 15 | 15 | OK |
| SCALAR | reputation | {} | {} | OK |
| 0 | status | claimed | claimed | OK |
| 0 | holder | idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz | idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz | OK |
| 0 | submission | b0d3bf66b97fd2192aab20e197d3a16295ff96773f1c4084856915eb74f64f8bi0 | b0d3bf66b97fd2192aab20e197d3a16295ff96773f1c4084856915eb74f64f8bi0 | OK |
| 0 | effective_pass | 0 | 0 | OK |
| 1 | status | claimed | claimed | OK |
| 1 | holder | idq18x8zm89zrmdf5susdgxtyg4lraf2z5rdejd984 | idq18x8zm89zrmdf5susdgxtyg4lraf2z5rdejd984 | OK |
| 1 | submission | 363791df12ebd17a399cc91f0c6b2db976e4075e2553eab03bbad38c550e65d6i0 | 363791df12ebd17a399cc91f0c6b2db976e4075e2553eab03bbad38c550e65d6i0 | OK |
| 1 | effective_pass | 0 | 0 | OK |
| 2 | status | claimed | claimed | OK |
| 2 | holder | idq1mgwl7ekmfdnu07lapcnjrja4r9pk66xxxxeflk | idq1mgwl7ekmfdnu07lapcnjrja4r9pk66xxxxeflk | OK |
| 2 | submission | ede75c27a3616b0006aee452df12e8c042ba94fc98b9314d9e44cfeb5aa0ce24i0 | ede75c27a3616b0006aee452df12e8c042ba94fc98b9314d9e44cfeb5aa0ce24i0 | OK |
| 2 | effective_pass | 0 | 0 | OK |
| 3 | status | claimed | claimed | OK |
| 3 | holder | idq1k8rd76nx2e7x0u9a7jls8q4n4ezp0vssw907mm | idq1k8rd76nx2e7x0u9a7jls8q4n4ezp0vssw907mm | OK |
| 3 | submission | 2bdcab82e5ba30fdcb6f02df5963842e685fcb8e19c28c97f8114358bdba5e65i0 | 2bdcab82e5ba30fdcb6f02df5963842e685fcb8e19c28c97f8114358bdba5e65i0 | OK |
| 3 | effective_pass | 0 | 0 | OK |
| 4 | status | claimed | claimed | OK |
| 4 | holder | idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5 | idq1d5m392ahkhp79wsy9ur79e3vhak7tg729dwdr5 | OK |
| 4 | submission | 6f36374fba1f1fe0d1db3896e8a78a5233703eb830b0f7b34199afecaba3f0fdi0 | 6f36374fba1f1fe0d1db3896e8a78a5233703eb830b0f7b34199afecaba3f0fdi0 | OK |
| 4 | effective_pass | 0 | 0 | OK |

## 三·B、第二条材料（线上 + 仓内爬取工具端到端复跑）

材料 1 用本机 scratch 脚本抓取。为证明**入仓的爬取工具本身可用**，用仓内
`internal/metatask/scripts/freeze_pools.py` 从零再抓一次（2026-09-16 19:5x +0800），
并让三个实现对同一份新料重跑：

| 项 | 值 |
|---|---|
| 七池条数 | task 2 / tree 2 / spec 23 / claim **121** / release 9 / submission 118 / verify 247（共 522 枚唯一 pin） |
| 载荷验收 | `OK` 522 / 拒收 0（`content_state_counts={'OK': 522}`） |
| 七池终止形态 | 全部 `LIST_NULL_EMPTY_CURSOR`（显式空页 + 空游标） |
| 新快照 manifest sha256 | `c67a33148dceb1866a38966667aa3da1bdde08a15b300cc10fcde221ecc87fd6` |
| pin 集合 sha256 | `4ee312d080ad2382a4577ef909db0ba4e7b6adfafa6853b3f954f6ff4d6ebb1d` |
| 引擎 ↔ Go 实现 | 111 节点，**差异 0** |
| 引擎 ↔ Python 复算 | 111 节点，**差异 0** |

对照材料 1（19:31 +0800）：claim 117 → 121，其余池不变 —— 池是活的，读数必须带抓取时刻。

## 四、第三方锚（另一实现者发布的冻结读数）

[pin://2f6c7857cbb76aa6c8762016349bac386006d62934100ae5dfc81396daa620e0i0](pin://2f6c7857cbb76aa6c8762016349bac386006d62934100ae5dfc81396daa620e0i0)
（Relict / 5F-Studio，重放时刻 2026-09-16 02:30Z，引擎同为 v1.2.0）公布了 7 个节点的冻结读数。
该件**只覆盖其本机提交的 7 个节点**（全树 111 节点，其自述「本机正是这 7 个节点的提交者」），
属范围差异、非结论冲突。我的冻结快照（11:31Z）对同样 7 行的比对：

| 节点 | 第三方 状态 / submission | 本模块 状态 / submission | 第三方 有效 pass/fail/invalid | 本模块 valid_pass/fail/invalid | 判定 |
|---|---|---|---|---|---|
| t55 | verified / c84ebd776aac… | verified / c84ebd776aac… | 2/0/2 | 2/0/2 | 一致 |
| t64 | claimed / 2906b6f6a0c6… | claimed / 2906b6f6a0c6… | 0/0/2 | 0/0/2 | 一致 |
| t66 | claimed / f8673f70fe98… | claimed / f8673f70fe98… | 0/0/4 | 0/0/4 | 一致 |
| t67 | claimed / 7ca64873dd31… | claimed / 7ca64873dd31… | 0/0/4 | 0/0/4 | 一致 |
| t68 | claimed / 63513439838c… | claimed / 63513439838c… | 0/0/4 | 0/0/4 | 一致 |
| t69 | claimed / 4c698efdf8a0… | claimed / 4c698efdf8a0… | 0/0/4 | 0/0/4 | 一致 |
| t70 | claimed / e2e2a4d89d56… | claimed / e2e2a4d89d56… | 0/0/4 | 0/0/4 | 一致 |

第三方件 sha256（原文取自 `/content/<pinId>`）：`800ee5955f901f0689312b14a16678be032e8668b75bb49f591ad43c59095ed7`。

## 五、差异项与原因（本步实际发生的三处）

三处都不是「最终结论差异」，而是**过程中真实咬到的分歧**，逐条记原因：

1. **本实现的 `verify_votes` 初值 11 vs 引擎 15（已定位并修正，现 0 差异）**。
   原因：引擎的票有效性判定是 Python 的 `str(value or "").strip()`，即**任意非空值算存在**；
   verify 池有 **4 枚票把 `semantic_check` 发成 JSON 数组**（如
   `["quantifier_shape","load_bearing_error","citation_binding"]`，其中 2 枚同出
   `idq1gjdmu2xz7jratgn8thqtjja3py80vynayhv0q7`）。我的第一版只认字符串，把这 4 票判为
   `missing_semantic_check`。处置：按引擎口径逐字对齐（`payloadPresent`），并新增向量
   `S1-array-semantic-check-counts` 锁死；同时在票级读数里输出 `semantic_check_type`，
   让「该字段并非统一为字符串」这件事可见。
   **待上游定夺**：`semantic_check` 是否应为字符串——若是，这 4 票在协议上应属畸形件。
2. **分页接口的 `total` 与实枚举条数不等（未解决，已按「不采信 total」处理）**。
   实测：claim `total=105` 而实枚举 117（其中 9 枚未确认件）；verify `total=237` 而实枚举 247。
   差额机制未查明（已排除：operation、chain、version、重复 id、`number>=0`）。因此完成判据
   **只认游标与显式空页**，`total` 一律带抓取时刻并排引用，不单独作分母。
3. **末页形态 `{"list":null,"nextCursor":""}`（假否定陷阱，已按拒收规则处理）**。
   实测：七池末页均返回 `list:null` + 空游标。若把它当「有游标但空件」就会误判为中断；若把
   任意 `list:null` 当池尽，则「空件 + 游标仍在」这种情况（向量 N6）会被静默接受为完整读数。
   本实现的判据：**只有显式空页 + 空游标才算收口**，其余六种停止条件全部标 UNKNOWN 并阻断
   「完整」标记。

### 五·B、真实字节负控：N1 在链上原件上落地

向量文件里的 N1 是合成件。为确认拒收规则不是只在合成件上成立，本步对**链上原件**做了一次负控：
取试点 #02 中 t55 的生效 submission（`c84ebd776aac1b4030dc07c7e205b082a8e3eaaac95c5929968321c0993d95c8i0`，
声明 `contentLength=2012`），把落盘字节截成 1006 字节后重跑两侧实现：

| 观测 | Go 实现 | Python 复算 |
|---|---|---|
| 拒收计数 | `pins_rejected=1` | `pins_rejected=1` |
| 拒收记录 | `{{"pin": "c84ebd77…95c8i0", "state": "short_item", "reason": "short item: declared contentLength=2012 actual bytes=1006 (channel=content)"}}` | 同 |
| t55 结算 | 由 `verified`（effective_pass 2）→ `claimed`（submission 丢失、effective_pass 0） | 同 |

即：短件被拒收且**没有**静默进入结算——节点状态如实降级，两侧读数一致。
（顺带说明：拒收件按池记录、不按节点归属——载荷本身读不出 node，把它摊到每个节点上
是错误归因；这是本步自查中修掉的一处实现缺陷。）

## 五·C、本步自查修掉的两处实现缺陷（不是引擎差异，是我自己的）

1. **拒收计数重复累加**：`BuildAggregate` 与命令层各自计一遍拒收件，输出 2 而实际 1
   （Python 侧 1）。已把 Meta 计数收敛为单一来源（`BuildAggregate`）。
2. **拒收件按节点摊派**：曾在 111 个节点上各列同一条拒收 pin。已改为池级 `rejected_records`
   （pin + state + reason），因为载荷读不出的记录无法归属节点。
两处都由「真实字节负控」暴露，均由两侧读数互相校验发现。

## 六、顺带得到的两个实质读数（不是差异，是结论）

1. **试点 #01 全票失效**：在该根下 16 枚 verify 票**全部**缺 `semantic_check`，按 v1.2.0 #9
   计票无效 → 有效 pass 0，5 个节点全部停在 `claimed`（引擎与我的读数一致）。这是 chair
   定案③「存量事件统一按新语义重放」的直接后果，此处只报事实。
2. **「水位」必须带时刻**：同一任务根，10:30Z 的公开读数记 verify 池 178 枚，11:31Z 冻结为
   247 枚，11:40Z 线上再抓为 522 枚总量（claim 117→121）。池是活的，单点数字不可跨时刻比较。

## 七、复现命令与产物哈希

```bash
# 1) 抓取冻结快照 + 组装引擎事件目录（contentBody 写原始字节，不用 contentSummary）
python3 internal/metatask/scripts/freeze_pools.py --out /tmp/mt-snapshot --events /tmp/mt-events
# 3) 引擎读数（vendored v1.2.0 脚本）
python3 internal/metatask/testdata/engine-fixtures/metatask_replay.v1.2.0.py --events /tmp/mt-events --root <pinId>
# 4) 本实现读数
CGO_ENABLED=0 go run ./cmd/metaso-p2p-metatask-pools --snapshot /tmp/mt-snapshot --root <pinId> --now <unix> --out go.json
# 5) Python 独立复算 + 逐项对照
python3 internal/metatask/scripts/recompute_pools.py recompute --snapshot /tmp/mt-snapshot --root <pinId> --now <unix> --out py.json
python3 internal/metatask/scripts/recompute_pools.py compare --a engine.json --b go.json
```

| 产物 | sha256 |
|---|---|
| `internal/metatask/scripts/freeze_pools.py` | `9457fdd977f5d63af8eb622a999fb25b250a338f08758074249071a833421d72` |
| `internal/metatask/scripts/recompute_pools.py` | `7b88124ca1bde742c4b2f0b67fd8773d2fbf942b70c738419a898fd4d1b22da1` |
| `internal/metatask/testdata/metatask-pools-vectors.v1.json` | `ea63945883355742c6605714015c5227c2308f4c4c8ad4e66da86f63ceb08210` |
| `internal/metatask/testdata/known-nodes.v1.json` | `d9f93df6e6320c49e12f819e6a15f8cca816df29392c0c02dc10f6f731617159` |
| 引擎脚本（v1.2.0，仓内 vendored） | `828a9b95b1f2c9648ca250122cd78063d622202ac53b776508c737a639cf3f25` |
| 引擎技能包 zip | `df9915ae4558523cb514c5098013b11156c2807c77f6837bfb8d3747d7238243` |
| 引擎读数 p02 / p01 | `ff8ba64ba82d384ca30914c57e949edfdd21517c8c13a9764e94a98574dad0eb` / `dc5d4fcec4ca3c9f7d54c4627b03253dadbd169548f2e82708d84040cf40c7ce` |
| Go 读数 p02 / p01（定稿提交重跑） | `143570d90249a19731edcb69713cd0ea85c2162797e07d6885f10dc54ebfdcd1` / 见 README §4 复现命令 |
| Python 读数 p02 / p01 | `42695df8ae98bb4a21b254a32ed73accb6015fb7470094644f1f071125fed5b1` / `3433a8cf5ddd97cbf036f3b6713d6b0214f2047b04abcce7be5398ab3a5ba415` |

### 七·B、哈希口径声明

上表哈希取自**定稿提交**（`fe9dad9`）后的重跑：`recompute_pools.py` 因修复「拒收件按节点摊派」而
变更（`c4436cf1…` → `4854c36a…`），§二/§三/§三·B 的对照结论也在同一提交上重跑确认（差异仍为 0）。
任何先于该提交的哈希不再作为证据。

### 七·C、canonical JSON 输出与已知节点向量（本轮，§十）

本轮把 `recompute_pools.py` 的 stdout / `--out` 改为 **canonical JSON**（`sort_keys` +
紧凑分隔 `(",",":")` + `ensure_ascii=False`，末尾一个换行），并新增 `expect` 子命令与
`known-nodes.v1.json`。因此 `recompute_pools.py` 的 sha256 变为 `88b82ad8…`（上表已更新），
**§七 表中先前的一切 Python 读数哈希（p02 / p01）随之作废**——它们绑定的是旧脚本的缩进排版。
Go 命令的排版未改（仍是缩进 JSON，供人读）；两种排版不做字节级互比，对照一律走 `compare`。
本轮新料与全部读数哈希见 **§十**。

### 七·D、双口径（semantic_check 过滤）改动后的哈希口径（本轮，§十一）

本轮为 `recompute_pools.py` 增加两口径开关（`--dual-report` / `--no-semantic-check-filter`）、
`ballot_inventory` / `caliber_view` / `node_diff` 投影，并给 `expect` 加 `--caliber`；
`metatask-pools-vectors.v1.json` 增 `caliber_vectors`（4 条），`known-nodes.v1.json` 增
`expect_ruling`（t35/t47/t55）。因此：

- `recompute_pools.py` sha256 变为 `7b88124c…`（上表已更新），
  `metatask-pools-vectors.v1.json` 变为 `ea639458…`，`known-nodes.v1.json` 变为 `d9f93df6…`；
- **任何先于本轮提交的脚本/向量哈希不再作为证据**（§七·C 引用的 `88b82ad8…` 已过期）；
- 默认路径（不带新开关）的 `recompute` 输出**逐字节未变**——回归门为
  `sha256(impl 读数) = 6988c0844d4db63f33a2ada1c2f463cef24fe55e4389d7f5c7a21f8c78fdd486`
  （19:31 快照），改动前后 `cmp` 相同；`expect`（默认 `--caliber implementation`）输出与退出码亦未变。

## 八、验收判据对照

| 判据 | 状态 | 证据 |
|---|---|---|
| 功能：与引擎逐项一致（#02 + 历史根） | 通过 | §二 448 字段 / §三 24 字段 / §三·B 448 字段，差异 0；另有第三方锚 §四 7/7 一致 |
| 分页：超单页仍全量枚举，截断按 §〇 拒收 | 通过 | verify 247 枚跨 6 页翻到显式空页（两条材料各跑一次）；向量 N5/N6/N7 锁三种非收口形态；载荷 N1 短件拒收 |
| 交付：代码 + 脚本 sha256 + 自测向量 + 对照表 | 通过 | 分支 `metatask-pool-aggregation`（未 push main）；§七 哈希表 |
| 输出：脚本 stdout 为 canonical JSON（`sort_keys` + 紧凑分隔 + `ensure_ascii=False`） | 通过 | §十：stdout 与 `--out` 逐字节相同；同材料重跑同 sha256 |
| 自测向量：≥3 个已知节点给出期望结算 | 通过 | §十：`known-nodes.v1.json`（t35 / t47 / t55），`expect` 对 Go / Python 读数各 3/3 通过；突变负控按预期 FAIL |
| 封闭测试：无新外部厂商 API / 无人类代写 | 通过 | 仅用仓库既有 MANAPI 端点；**新增依赖 0**（Go 标准库 + Python 标准库 + 系统 curl），登记见 README §4.1 |
| 独立验证 / 跨 owner 复核 | **未完成** | 节点级实现步，跨 owner 复核待外部方执行 |

## 九、已知未覆盖项

1. 真实交易手续费不可得（索引器无交易费字段；`genesisFee` 本次全为 0）。
2. `total` 语义未判定（见 §五.2）。
3. verify 池 48/247 枚 `status=1`，语义未判定、未据此过滤。
4. 未接 HTTP 路由（本步交付包 + 命令行读数，未改 `main.go`/config）。
5. 跨 owner 独立复核未进行。

## 十、canonical JSON 输出与已知节点向量（本轮补强）

本轮两处改动（`recompute_pools.py` sha256 `4854c36a…` → `88b82ad8…`）：

1. **stdout 改为 canonical JSON**：`recompute_pools.py recompute` 的 stdout 与 `--out` 现在同为
   `json.dumps(..., ensure_ascii=False, sort_keys=True, separators=(",", ":"))` + 一个换行。
   实测同材料下 stdout 与 `--out` **逐字节相同**（`cmp` 无差异），读数哈希因此可复算。
2. **新增 `expect` 子命令 + `known-nodes.v1.json`**：把三个已知节点的期望结算钉成机器可核对向量
   （t35 同块 txIndex 决胜 / t47 release→重 claim / t55 quorum 有效票）。每个期望字段必须**在报告里存在**：
   字段缺失（`ABSENT`）与值不同（`DIFF`）分开报，缺失即 FAIL——报告 schema 静默变窄时不会悄悄通过。

本轮**零链上写入**，全部为只读取件与本地复算。

### 十·1、本轮材料（独立重抓，不沿用 §一）

| 项 | 值 |
|---|---|
| 抓取时刻 | 2026-09-16 19:49:46 +0800（`frozen_at_unix=1789559386`） |
| 七池条数 | task 2 / tree 2 / spec 23 / claim 123 / release 9 / submission 122 / verify 247（共 528 枚唯一 pin） |
| 七池终止形态 | 全部 `LIST_NULL_EMPTY_CURSOR`（游标翻到显式空页） |
| 载荷验收 | `OK` 528 / 拒收 0（`content_state_counts={'OK': 528}`） |
| manifest sha256 | `7b3e180b3fba6faa6aca77086c957490b8ae2e60bc8d1b443b05022ac1cf6a8d` |

### 十·2、三实现逐项一致（新料）

任务根 `08cac496dfa93874dd7d16893038da16b0d2dbc92f844d09512ca0cc78c03b46i0`；对照字段同 §二。

| 对照 | 节点数 | 对照字段 | 差异 |
|---|---|---|---|
| 引擎 v1.2.0 ↔ Python 复算 | 111 | 448 | **0** |
| 引擎 v1.2.0 ↔ Go 实现 | 111 | 448 | **0** |

本轮读数 sha256：引擎 `b38e8b8fe17ccbd022c5d13fa220ee938b8756a938c701dfad538b7538a88bfd`、
Go `6d5bc487cb1a5db34d77db900ab69c1ece928cef5700d8f50a87ad500100576f`、
Python `98b62db6b126385df55b02887b1d998ac9bc530a6df85389cf9706248b0e703e`。
（三者字节不同：引擎与 Go 为缩进排版、Python 为 canonical；一致性由 `compare` 按字段判定，不做字节互比。）

### 十·3、已知节点向量核对（t35 / t47 / t55）

期望值由本轮材料独立复算得到，三实现一致；`expect` 结果：

| 节点 | 状态 | 生效 claim（`holder_pin`） | submission | claimid 配对 | 有效 pass / invalid |
|---|---|---|---|---|---|
| t35 | claimed | `b4f7c225807b2256e8a98cde093df9d60c2e277cc671b829a1093ca83eca6ea8i0` | `7ba5c68905232b9d3a044bee019901d05d6a0c7957f27bb2b4bc5d4b1ac6cd02i0` | ✓ 同 pin | 0 / 2 |
| t47 | claimed | `ba2824f9efaf3f80817204d335173dfbd11a32907ab78e1e263da0abc0270f59i0` | `b0d1a9d3995d7c4294c8b780f6f367b86c09ef5a8d4ae6aafd3c7494e21710f0i0` | ✓ 同 pin | 0 / 6 |
| t55 | verified | `4dd439fff2558f60c630269bc9f0c5c77e00f4a1abd5e6a6c4c87dee1d2de753i0` | `c84ebd776aac1b4030dc07c7e205b082a8e3eaaac95c5929968321c0993d95c8i0` | ✓ 同 pin | 2 / 2 |

- `expect --report <Go 读数>` → `3 passed, 0 failed`；`expect --report <Python 读数>` → `3 passed, 0 failed`。
- **负控**：把 Python 读数里 t55 的 `effective_pass` 改成 1 再跑 →
  `t55 FAIL effective_pass=DIFF 1 want 2`，退出码 1。该核对不是恒真式。

### 十·4、链序证据（三节点各覆盖一条机制）

按 `(genesisHeight, txIndex)` 重排链上原件：

| 节点 | 事件（链序） | 机制 |
|---|---|---|
| t35 | claim h=189772 tx=**62** `b4f7c225…` → tx=72 `1eeff420…` → tx=86 `da70ef56…` | 同块 FCFS：最小 txIndex 持锁，后两张被忽略 |
| t47 | claim h=189544 `34bf4aec…` → claim h=189545 `9c23232c…`（被忽略）→ release h=189570 tx=1 `4b25ec61…`（撤 `34bf4aec…`）→ claim h=189570 tx=3 `ba2824f9…` | release→重 claim：新 claim 接锁；已忽略的 `9c23232c…` 不因 release 复活 |
| t55 | 4 张 verify 票全 pass，其中 2 张缺 `semantic_check`（#9 不计票） | quorum：仅 2 张有效 pass，且来自 ≠ 提交者 ≠ 任务根作者 |

第三方通道对照：三枚 submission pin 直接从 `https://manapi.metaid.io/content/<pinId>` 再取一次，
`node` / `taskid` / `claimid` 与快照内字节一致（t55 载荷 2012 字节）。

## 十一、双口径读数与「受影响票清单」（试点 #02 · semantic_check 过滤）

### 十一·1、裁定依据与两口径定义

- 裁定件：[pin://1bf3cb15470a45655fc3e2a52f1a309b04a8f8fd86989243f9f964eb39d7988ci0](pin://1bf3cb15470a45655fc3e2a52f1a309b04a8f8fd86989243f9f964eb39d7988ci0)
- 澄清件：[pin://6adaa70088f0fb136eda7a5f314ac61c88830b8ba63ece72d8b5091cc0e07af8i0](pin://6adaa70088f0fb136eda7a5f314ac61c88830b8ba63ece72d8b5091cc0e07af8i0)

裁定要点（逐字取自链上）：`semantic_check` **不是 spec 条款**——spec v1.0→v1.2.11 从未写入该必填
字段；该过滤来自 metatask-replay 的**实现自述**与修订清单 #9 的缺项裁定，**不追溯、不作 spec 级
计票规则**；凡**仅因缺该字段被扣**的票**恢复计入**（其余计票条件满足时）。判决记录须并列两套读数。

| 口径 | 定义 | 开关 |
|---|---|---|
| (a) 实现口径 | 含 `semantic_check` 过滤（工具当前真实行为，即 replay v1.2.0 的 #9） | 默认 |
| (b) 裁定口径 | 不含该过滤（协议应有行为）；#8 的「fail 缺 failreason」判据**不变** | `--no-semantic-check-filter` |

命令：

```bash
# 两套读数 + 受影响票清单（canonical JSON 到 stdout；--out 时另写 .sha256）
python3 internal/metatask/scripts/recompute_pools.py recompute --snapshot <dir> \
    --root 08cac496dfa93874dd7d16893038da16b0d2dbc92f844d09512ca0cc78c03b46i0 \
    --now <unix> --dual-report --out affected-ballots-pilot02.json
# 单口径（b）读数
python3 internal/metatask/scripts/recompute_pools.py recompute --snapshot <dir> --root <pinId> \
    --now <unix> --no-semantic-check-filter
```

### 十一·2、材料（两个冻结快照，结论一致）

| 快照 | 抓取时刻 | manifest sha256 | 七池翻页 | verify 票 |
|---|---|---|---|---|
| A（§十 已留档） | 2026-09-16T19:31:48+0800 | `104474d99cbb0b2c23127bd1a32a328ad2ac6155316650a8e0dcb632c058b2b8` | 七池皆到显式空页 | 247 |
| B（本轮新冻结） | 2026-09-16T21:13:13+0800（`frozen_at_unix=1789564393`） | `9b305aebcc3a10b0a53b6914eac2d70b804e2bd6fb678a6d953fd2fc66ef43c5` | 七池皆 `LIST_NULL_EMPTY_CURSOR`，531/531 载荷 `ok`，0 拒收 | 248 |

任务根 `08cac496dfa93874dd7d16893038da16b0d2dbc92f844d09512ca0cc78c03b46i0`（111 节点，quorum 2）。
池是活的：A→B 涨 8 枚 pin（claim 117→123、submission 118→124、verify 247→248）。
**两个快照的双口径结论逐项相同**（见十一·3 / 十一·5），但清单哈希不同——清单绑定材料。

> 材料获取旁证：本轮另有一份 20:59 的线上抓取（`scratch/poolcheck`）在 verify 第 3 页
> **仍带 live cursor**（48 条 + 非空 `nextCursor`），故其 248 条是**截断读数**，不能当池尽。
> 正式材料一律用 `freeze_pools.py` 翻到**显式空页**的结果（判据见 §〇.1）。

### 十一·3、两套口径读数（快照 B）

| 字段 | (a) 实现口径 | (b) 裁定口径 | 变化 |
|---|---|---|---|
| quorum | 2 | 2 | 不变 |
| all_verified | False | False | 不变 |
| verify_votes（池级） | 16 | 235 | **变** |
| verify_votes_task_scoped | 13 | 203 | **变** |
| verify 池 invalid_votes | 216 | 9 | **变** |
| verify 池 effective / ignored | 13 / 219 | 203 / 29 | **变** |
| verify 池 records / records_in_scope | 248 / 232 | 248 / 232 | 不变 |
| reputation（命中 owner 数 / 票数合计） | 2 / 4 | 8 / 61 | **变** |

### 十一·4、受影响票清单

清单逐票列出**本任务根全部 submission 的票**（`ballots`，共 232 条；另有 16 条 targeting 他任务根，
计为 `ballots_out_of_scope`）。每条字段：`pin` / `target`（submission pin）/ `target_node` /
`target_effective` / `verifier` / `verdict_raw` / `original_verdict` / `deducted_for_missing_semantic_check` /
`ruling_verdict` / `effect` / `affected` / `counts_pass_{impl,ruling}` / `counts_fail_{impl,ruling}` /
`verifier_owner`。

| 指标 | 张数（快照 A / B） |
|---|---|
| 本根票（in scope） | 231 / 232 |
| **受影响**（两口径判定不同） | **215 / 215** |
| └ `effect=restored_to_valid`（恢复为有效票） | 207 / 207 |
| └ `effect=reason_only`（理由变、判定仍 invalid） | 8 / 8 |
| 不受影响（两口径判定一致） | 16 / 17 |
| 原判定 `invalid` | 216 / 216 |
| 裁定口径判定 `pass` | 222 / 223 |
| 裁定口径判定 `invalid`（#8 缺 `failreason`，两口径一致） | 9 / 9 |
| target 未生效的票（列出、不进节点计数） | 28 / 29 |
| 计入 quorum 的 pass 票 | 13 → **203**（两快照相同） |

### 十一·5、节点差异：哪些票数变、哪些 verified 状态变

| 项 | 快照 A | 快照 B |
|---|---|---|
| 节点数 | 111 | 111 |
| 票数变化节点 | 67 | 67 |
| **verified 状态变化节点（claimed → verified）** | **57** | **57** |
| 状态不变节点 | 44 | 44 |

57 个状态变化节点（两快照相同）：`m51 m61 m71 t31 t32 t33 t34 t35 t36 t37 t38 t39 t40 t41 t43 t45 t47
t50 t51 t52 t53 t54 t56 t57 t58 t59 t60 t61 t62 t63 t64 t65 t66 t67 t68 t69 t70 t71 t72 t73 t74 t76 t78
t80 t102 t111 t112 t116 t117 t119 t120 t121 t122 t126 t127 t128 t129`

票数变化但状态不变的 10 个节点：`t42 t44 t46 t49 t55 t75 t77 t79 t107 t113`。

> **口径落差（须 chair 知悉）**：裁定件预期「结论不应变」。实测**只有全局结论不变**——
> `all_verified` 两口径皆 `False`；但**节点级 verified 集合改变了 57 个节点**，reputation
> 命中 owner 由 2 家变 8 家。原因不是实现有误，而是「计票过滤」直接决定了哪些节点的
> `effective_pass` 能达 quorum：本池 216 张 in-scope 票中 207 张仅缺该字段，恢复后
> 大量节点恰好达标。**本步只报事实，不动引擎**。

### 十一·6、三个已知节点的双口径读数

| 节点 | 口径 | status | effective_pass | valid_pass_total | invalid_votes |
|---|---|---|---|---|---|
| `t35` | (a) 实现 | claimed | 0 | 0 | 2 |
| `t35` | (b) 裁定 | **verified** | **2** | **2** | **0** |
| `t47` | (a) 实现 | claimed | 0 | 0 | 6 |
| `t47` | (b) 裁定 | **verified** | **6** | **6** | **0** |
| `t55` | (a) 实现 | verified | 2 | 2 | 2 |
| `t55` | (b) 裁定 | verified（**不变**） | **4** | **4** | **0** |

- `t35` / `t47`：其 `0-pass` **确因缺 `semantic_check` 被扣**——裁定口径下分别恢复 2 / 6 张有效
  pass，**均跨 owner**（≠ 提交者 ≠ 任务根作者），达 quorum 2 → **claimed 变 verified**。
  这正是裁定澄清件三节「取决于其 invalid/0-pass 是否因缺该字段被扣」的待确认项，现以确数补入。
- `t55`：2 → 4 有效 pass，`verified` 结论不变——与裁定件「4 张应为有效、结论不变」**逐字一致**
  （对 chair 读数的正向对照）。
- 三个节点的读数在快照 A / B 上**完全相同**，且 `expect --caliber ruling` 在两快照上均 3/3 PASS。

### 十一·7、产出物与复算

| 产物 | 说明 | sha256 |
|---|---|---|
| `affected-ballots-pilot02.json` | 快照 B 的双口径清单（canonical JSON，241 485 B） | `68d15c680e086b19763c81eb2e349bc7c9b1f66be532dc3b3d9b8d2beec36234` |
| `affected-ballots-pilot02.snap1931.json` | 快照 A 的同形清单（连续性对照） | `f4e24b243fac3b977c42cee5b4627c75329ed35f2d927042a8d454339050b713` |
| `diff-summary.md` | 差异汇总（数字由上面清单派生） | `562fa1ac977e539f6581703ba8b395ff0d91dd31c521c3fe724ced7b70c32552` |
| 清单 stdout | 与 `--out` 文件**逐字节相同** | 同清单 sha256 |

清单内 `producer.script_sha256` = `7b88124ca1bde742c4b2f0b67fd8773d2fbf942b70c738419a898fd4d1b22da1`，
把读数绑到产出脚本的版本上。canonical 形式已独立复核：`json.dumps(loads(bytes),
sort_keys=True, separators=(",",":"), ensure_ascii=False)+"\n"` 与文件字节相等。

### 十一·8、自测与负控（本轮全部实跑）

| 检查 | 结果 |
|---|---|
| `vectors`（含新 `caliber_vectors` 4 条） | **21 passed, 0 failed** |
| 默认路径回归：`recompute` 快照 A / B 输出 vs 改动前读数 | **逐字节相同**（A: `6988c084…`） |
| `expect`（实现口径，快照 A / B） | 3 passed, 0 failed |
| `expect --caliber ruling`（快照 A / B） | 3 passed, 0 failed |
| 双口径清单 vs 单口径独立重跑（`--no-semantic-check-filter`）交叉核对 | 两口径节点投影**全等** |
| **负控 1**：`caliber_vectors` 把 C1 的裁定期望改 `claimed` | FAIL，退出码 1 |
| **负控 2**：把 C2 的 `invalid_reason_ruling` 改值 | FAIL，退出码 1 |
| **负控 3**：把实现口径读数拿去核对裁定期望 | 3 FAIL（t35/t47/t55），退出码 1 |
| **负控 4**：已知节点 t55 `effective_pass` 改 99 再跑 `expect` | FAIL，退出码 1 |

新增 `caliber_vectors`（`metatask-pools-vectors.v1.json`）：C1 缺 `semantic_check` 的票在裁定口径下恢复；
C2 `#8` 缺 `failreason` 两口径一致；C3 同时缺两项 → 理由变、判定不变；C4 恢复计入**不等于**自动达标
（同一 bot 两张票按 `(target,bot)` 仍只算一张）。

### 十一·9、已知未覆盖项

1. **`verifier_owner` 未参与独立性判定**：本实现用 pin 的 `globalMetaId` 判跨 owner（与引擎一致），
   而裁定件称 `verifier_owner` 是 v1.2-r1 起的 spec 必填字段。本池 232 条 in-scope 票中仅 194 条
   带该字段。**本步不改变该语义**（口径 (b) 只是去掉 `semantic_check` 一处过滤），差异已在清单
   `verifier_owner` 列留痕，供后续单独裁定。
2. **只覆盖试点 #02**：裁定澄清件提到「含试点 #01 历史根对照」，本步未做；同一快照下换 `--root`
   即可产出同形清单。
3. **口径 (b) 不改变 #8**：8 张同时缺两项的票在 (b) 下仍 invalid（理由改为 `missing_failreason`），
   这是有意为之，不是遗漏。
