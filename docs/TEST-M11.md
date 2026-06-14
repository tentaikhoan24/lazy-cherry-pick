# Kế hoạch test thủ công — M11 Advanced Pick (a + b + c)

> Áp dụng cho: **M11a** auto-stash · **M11b** squash + edit message · **M11c** partial-file pick.
> Tất cả đều cần chạy app thật (`.\dev.ps1`) — đây là phần Hard rule #1, AI không chạy được hộ.
> Sau mỗi mục test nên kiểm tra lại bằng terminal: `git log --oneline -5`, `git status`, `git stash list`.

---

## 0. Chuẩn bị

### 0.1. Chạy app
```powershell
.\dev.ps1
# hoặc: cd app ; npm run tauri dev
```

### 0.2. Tạo repo test sạch (nên dùng repo riêng, KHÔNG dùng repo thật)
```powershell
$T = "D:\project\test-m11"
Remove-Item -Recurse -Force $T -ErrorAction SilentlyContinue
git init $T ; cd $T
git config user.email t@t.com ; git config user.name tester

# main: 3 file
"main-a`n" | Out-File -Encoding utf8 a.txt
"main-b`n" | Out-File -Encoding utf8 b.txt
"main-c`n" | Out-File -Encoding utf8 c.txt
git add . ; git commit -m "base on main"

# feature: vài commit để cherry-pick
git checkout -b feature
"feat-1`n" | Out-File -Encoding utf8 d.txt;  git add . ; git commit -m "feat: add d.txt"
"feat-2`n" | Out-File -Encoding utf8 e.txt;  git add . ; git commit -m "feat: add e.txt"
Add-Content f.txt "new file f" ; git add . ; git commit -m "feat: touch a.txt + add f.txt" # commit đa-file
"feat-a-edit`n" | Out-File -Encoding utf8 a.txt ; git add . ; git commit -m "feat: edit a.txt (sẽ xung đột)"

git checkout main
```
- Nhánh **feature** có 4 commit. Commit "touch a.txt + add f.txt" có **2 file** (test partial pick).
- Commit "edit a.txt" **sẽ xung đột** nếu main cũng đổi a.txt — tạo xung đột bằng cách (tùy chọn) sửa a.txt trên main trước khi pick.

### 0.3. Mở repo trong app
- **Open repo** → chọn `D:\project\test-m11`.
- Source branch = `feature`, Target branch = `main`.

> 💡 **Reset nhanh giữa các test:** `git checkout main; git reset --hard <sha base>; git stash clear` (hoặc xoá tạo lại repo theo 0.2).

---

## 1. M11a — Auto-stash

**Bật tính năng:** Settings (⚙) → tab **General** → bật **"Auto-stash before apply"**. Mặc định OFF.

### TC-1.1 — Apply khi cây làm việc dirty (auto-stash ON)
1. Sửa 1 file đang theo dõi (vd thêm dòng vào `b.txt`) **mà không commit**. Có thể tạo thêm 1 file untracked (vd `scratch.txt`).
2. Tick 1 commit "feat: add d.txt" → Apply.
- ✅ Toast **"Stashed uncommitted changes — will restore after"**.
- ✅ Cherry-pick chạy bình thường, commit `d.txt` lên main.
- ✅ Sau khi xong: toast **"Restored your stashed changes"**; mở `b.txt` thấy thay đổi quay lại; `scratch.txt` vẫn còn.
- 🔎 Git Console: thấy `git stash push -u -m lcp-autostash` → các lệnh cherry-pick → `git stash pop ...`.
- 🔎 Terminal: `git stash list` rỗng.

### TC-1.2 — Apply khi cây dirty nhưng auto-stash OFF (hành vi cũ)
1. Tắt setting. Để cây dirty. Apply.
- ✅ Toast **lỗi** "working tree has uncommitted changes…" (mã `-32002`). Không có gì bị pick.

### TC-1.3 — Auto-stash + xung đột giữa chừng
1. (Tạo xung đột: trên main `git`-sửa `a.txt` khác feature, commit.) Bật auto-stash, để cây dirty.
2. Queue cả "feat: edit a.txt (xung đột)". Apply.
- ✅ Đã stash trước; cherry-pick dừng ở xung đột → ConflictResolver hiện.
- ✅ Stash **CHƯA** được pop (đang giữ). `git stash list` còn `lcp-autostash`.
3. Resolve xung đột → **Continue**.
- ✅ Sau khi cả luồng xong: toast "Restored…", stash được pop. `git stash list` rỗng.
4. Lặp lại nhưng bấm **Abort** thay vì Continue.
- ✅ Abort xong vẫn pop lại stash (khôi phục việc đang làm dở của bạn).

### TC-1.4 — Cancel giữa batch
1. Auto-stash ON + dirty, queue nhiều commit, Apply rồi bấm **Cancel** nhanh.
- ✅ Sau cancel, stash được pop lại.

### ⚠️ Chú ý M11a
- **Pop có thể xung đột:** nếu thay đổi đang stash đụng đúng vùng vừa cherry-pick → `git stash pop` xung đột → toast **đỏ** "Couldn't restore stash — … run `git stash pop` manually". Lúc này repo còn nguyên stash; tự xử bằng tay. (Đây là hành vi đúng, không phải bug.)
- Chỉ pop **đúng** stash tên `lcp-autostash` — nếu bạn có stash thủ công khác, nó **không** bị đụng.
- Auto-stash gồm cả file **untracked** (`-u`).

---

## 2. M11b — Squash

> Checkbox **"Squash into one"** chỉ hiện khi queue có **≥ 2 commit**.

### TC-2.1 — Squash cơ bản (không xung đột)
1. Queue 2–3 commit không đụng nhau (vd "add d.txt", "add e.txt").
2. Tick **Squash into one** ở đầu hàng đợi.
- ✅ Hiện ô textarea message, mặc định = subject của các commit (mỗi dòng 1 cái).
- ✅ Nút Apply đổi nhãn thành **"Apply & Squash N commits → main"**.
3. Sửa message thành "squashed: d + e". Apply.
- ✅ Toast "Squashed picked commits into one".
- 🔎 `git log --oneline -3`: chỉ **1 commit mới** "squashed: d + e" trên main (không phải 2).
- 🔎 `git show --stat HEAD`: chứa cả `d.txt` và `e.txt`.

### TC-2.2 — Squash + xung đột giữa chừng
1. Tạo xung đột (như TC-1.3). Queue gồm vài commit + commit xung đột. Bật squash.
2. Apply → dừng ở xung đột → resolve → **Continue**.
- ✅ Sau khi cả luồng xong, **tất cả** commit (gồm cái vừa resolve) gộp thành **1**. `git log` xác nhận.
- 🔎 Mấu chốt: base được chụp **trước** batch nên squash đúng kể cả khi giữa chừng có xung đột/skip.

### TC-2.3 — Squash khi có commit bị skip (đã áp dụng)
1. Pick 1 commit, rồi pick lại cùng commit đó + 1 commit mới, bật squash.
- ✅ Commit đã áp dụng bị skip (toast), phần còn lại vẫn squash đúng thành 1.

### TC-2.4 — Squash chỉ 1 commit
- ✅ Queue 1 commit → checkbox squash **không hiện** (vô nghĩa). Apply thường.

### ⚠️ Chú ý M11b-squash
- Squash chạy **sau khi** cả luồng pick xong, **trước khi** pop auto-stash (đúng thứ tự).
- Nếu **Abort/Cancel** giữa chừng → **không** squash (batch dở không bị gộp).
- Message rỗng → tự dùng "Squashed commit".

---

## 3. M11b — Edit message (sửa message từng commit)

> Nút **✎** trên mỗi dòng queue, chỉ khi **KHÔNG** bật squash.

### TC-3.1 — Sửa message rồi apply
1. Queue 2 commit. Bấm **✎** ở commit đầu → hiện ô input.
2. Gõ message mới "reworded: my new message". Enter (hoặc click ra ngoài).
- ✅ Dòng đó hiện message mới **màu hổ phách + nghiêng**, kèm badge ✎ cạnh SHA.
3. Apply.
- 🔎 `git log` trên main: commit đó có message "reworded: my new message"; commit kia giữ nguyên.

### TC-3.2 — Reset override
- Mở lại ✎ → bấm nút **↺** → message quay về gốc (mất màu/badge).

### TC-3.3 — Edit message + xung đột
1. Override message cho commit **sẽ xung đột**. Apply → dừng xung đột → resolve → Continue.
- ✅ Commit sau khi continue mang **message override** (amend sau `--continue`). `git log` xác nhận.
- ⚠️ Nếu commit bị **skip** (resolve khiến nó rỗng) → override bị bỏ qua (không có commit để sửa) — đúng.

### TC-3.4 — Edit ẩn khi squash
- ✅ Bật squash → nút ✎ trên các dòng **biến mất** (vì sẽ gộp, message riêng vô nghĩa).

---

## 4. M11c — Partial-file pick

> Ở **bảng chi tiết** (click 1 commit ở danh sách nguồn) → danh sách file có **checkbox** + footer.

### TC-4.1 — Pick một phần file của commit đa-file
1. Click commit "feat: touch a.txt + add f.txt" (commit 2 file).
2. Tick **chỉ** `f.txt` (bỏ `a.txt`). Footer hiện "1 selected" + nút **"Pick 1 file → main"**.
3. Bấm nút.
- ✅ Toast "Picked 1 file from \<sha\> → main".
- 🔎 `git show --name-only HEAD`: **chỉ** `f.txt`; `a.txt` KHÔNG bị đụng trên main.
- 🔎 Commit message = message gốc của commit (reuse qua `-C`), author gốc giữ nguyên.

### TC-4.2 — File added / deleted
- Commit có file **mới** (added): bỏ tick nó → file đó **không** xuất hiện trên main (đã revert sạch).
- Commit **xoá** file: nếu bỏ tick file bị xoá → file đó vẫn còn trên main (không bị xoá).

### TC-4.3 — Chọn hết / chọn 0
- ✅ Tick **tất cả** file → nút **disabled** (gợi ý: dùng Apply thường). 
- ✅ Không tick gì → nút disabled.

### TC-4.4 — Partial pick gặp xung đột
1. Click commit "feat: edit a.txt (xung đột)" (a.txt đã đổi trên main). Tick `a.txt` → Pick.
- ✅ Toast **lỗi** "partial pick: commit conflicts with the target; pick the whole commit to resolve conflicts".
- 🔎 `git status` **sạch** (đã `reset --hard` discard) — không để lại rác. Không có commit mới.
- 👉 Cách xử lý đúng: pick **cả commit** qua hàng đợi để dùng ConflictResolver.

### TC-4.5 — Partial pick + auto-stash
- Bật auto-stash, để cây dirty, partial pick → stash trước → pick → pop sau (giống M11a).

### ⚠️ Chú ý M11c
- Partial pick tạo commit **ngay** lên target đang chọn (không qua hàng đợi).
- Chỉ xử lý commit **không xung đột**; xung đột thì báo lỗi và để repo sạch.
- File untracked do `cherry-pick -n` tạo ra rồi bị loại: đã dùng `git restore` nên được dọn; nếu thấy file lạ sót lại sau case lỗi, báo lại (edge hiếm).

---

## 5. Test phối hợp (regression — đảm bảo không vỡ luồng cũ)

| TC | Kịch bản | Mong đợi |
|----|----------|----------|
| 5.1 | Apply thường (không bật gì) | Như trước M11 — không đổi |
| 5.2 | Apply & Push (không squash) | Push bình thường |
| 5.3 | Apply & Push & Create PR + squash | Pick → squash → push → dialog PR mở (title từ commit squash) |
| 5.4 | Squash **và** edit-message cùng lúc | edit-message bị bỏ qua (squash thắng); chỉ ra 1 commit với message squash |
| 5.5 | Auto-stash + squash + xung đột | stash trước → resolve → squash → pop (đúng thứ tự) |
| 5.6 | Mở/đổi repo giữa chừng | State squash/override/stash reset gọn, không rò sang repo khác |

---

## 6. Checklist tổng (đánh dấu khi xong)

- [ ] 1.1 Auto-stash apply dirty → stash + restore
- [ ] 1.2 Auto-stash OFF + dirty → lỗi đúng
- [ ] 1.3 Auto-stash + xung đột → giữ stash, pop sau continue/abort
- [ ] 1.4 Cancel → pop lại
- [ ] 2.1 Squash cơ bản → 1 commit
- [ ] 2.2 Squash + xung đột → gộp đúng
- [ ] 2.3 Squash + skip
- [ ] 2.4 Squash ẩn khi 1 commit
- [ ] 3.1 Edit message → log đúng
- [ ] 3.2 Reset override
- [ ] 3.3 Edit message + xung đột → amend sau continue
- [ ] 3.4 Edit ẩn khi squash
- [ ] 4.1 Partial pick 1 file
- [ ] 4.2 Added/deleted file
- [ ] 4.3 Chọn hết/0 → disabled
- [ ] 4.4 Partial pick xung đột → lỗi + repo sạch
- [ ] 4.5 Partial pick + auto-stash
- [ ] 5.1–5.6 Regression phối hợp

---

## 7. Khi gặp lỗi — thu thập thông tin

1. **Git Console** trong app (nút `>_`) — chụp lại chuỗi lệnh git của thao tác lỗi.
2. Terminal đang chạy `tauri dev` — copy log Rust/Vite (nếu có panic/stack).
3. `git status` + `git log --oneline -8` + `git stash list` ngay sau lỗi.
4. Mô tả: bật setting gì, queue gì, target gì, bấm nút nào.
