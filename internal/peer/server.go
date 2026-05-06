package peer

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"p2p/internal/config"
	"p2p/internal/models"
)

func Run(cfg config.Config) error {
	storage := NewStorage(cfg)
	if err := storage.InitDirs(); err != nil {
		return err
	}
	authStore, err := NewAuthStore(cfg.DatabaseURL, cfg.SessionTTL)
	if err != nil {
		return err
	}

	trackerClient := NewTrackerClient(cfg)
	downloader := NewDownloader(cfg)

	if err := trackerClient.RegisterPeer(context.Background()); err != nil {
		log.Printf("tracker register warning: %v", err)
	}

	local, err := storage.ListLocalFiles()
	if err == nil {
		for _, meta := range local {
			if err := trackerClient.AnnounceFile(context.Background(), meta); err != nil {
				log.Printf("announce warning for %s: %v", meta.ID, err)
			}
		}
	}

	go heartbeatLoop(cfg, trackerClient)

	app := fiber.New(fiber.Config{
		BodyLimit: 1024 * 1024 * 1024, // 1GB for larger video uploads.
	})

	app.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "mode": "peer", "peer_id": cfg.PeerID})
	})

	app.Get("/", func(c fiber.Ctx) error {
		localFiles, _ := storage.ListLocalFiles()
		networkFiles, _ := trackerClient.ListFiles(c.Context())
		return c.Type("html").SendString(buildHTML(cfg.PeerID, localFiles, networkFiles))
	})

	app.Post("/api/v1/auth/register", func(c fiber.Ctx) error {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.Bind().Body(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		req.Username = strings.TrimSpace(req.Username)
		if len(req.Username) < 3 || len(req.Password) < 6 {
			return fiber.NewError(fiber.StatusBadRequest, "username >= 3 chars and password >= 6 chars")
		}
		if _, err := authStore.RegisterUser(c.Context(), req.Username, req.Password); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "username already exists")
		}
		token, user, err := authStore.Login(c.Context(), req.Username, req.Password)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(fiber.Map{"status": "registered", "token": token, "user": user})
	})

	app.Post("/api/v1/auth/login", func(c fiber.Ctx) error {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.Bind().Body(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		token, user, err := authStore.Login(c.Context(), req.Username, req.Password)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "invalid credentials")
		}
		return c.JSON(fiber.Map{"status": "logged_in", "token": token, "user": user})
	})

	api := app.Group("/api/v1", authMiddleware(authStore))

	api.Get("/auth/me", func(c fiber.Ctx) error {
		user := c.Locals("user").(User)
		return c.JSON(fiber.Map{"user": user})
	})

	api.Post("/auth/logout", func(c fiber.Ctx) error {
		token := extractBearerToken(c.Get("Authorization"))
		if token != "" {
			_ = authStore.Logout(c.Context(), token)
		}
		return c.JSON(fiber.Map{"status": "logged_out"})
	})

	api.Get("/files/local", func(c fiber.Ctx) error {
		localFiles, err := storage.ListLocalFiles()
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(localFiles)
	})

	api.Get("/files/network", func(c fiber.Ctx) error {
		files, err := trackerClient.ListFiles(c.Context())
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		return c.JSON(files)
	})

	api.Get("/me/actions", func(c fiber.Ctx) error {
		user := c.Locals("user").(User)
		actions, err := authStore.ListActions(c.Context(), user.ID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(actions)
	})

	api.Post("/upload", func(c fiber.Ctx) error {
		fileHeader, err := c.FormFile("file")
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "expected multipart file field 'file'")
		}

		meta, err := storage.SaveUploadedFile(fileHeader)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := trackerClient.AnnounceFile(c.Context(), meta); err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		user := c.Locals("user").(User)
		_ = authStore.AddAction(c.Context(), user.ID, "upload", meta.ID, meta.Name, meta.SizeBytes)
		return c.JSON(fiber.Map{"status": "uploaded", "file": meta})
	})

	api.Post("/download/:id", func(c fiber.Ctx) error {
		fileID := c.Params("id")
		details, err := trackerClient.GetFileDetails(c.Context(), fileID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		chunks, err := downloader.DownloadFile(c.Context(), details)
		if err != nil {
			return fiber.NewError(fiber.StatusBadGateway, err.Error())
		}
		meta, path, err := storage.SaveDownloadedFile(details.File, chunks)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := trackerClient.AnnounceFile(c.Context(), meta); err != nil {
			log.Printf("announce after download warning: %v", err)
		}
		user := c.Locals("user").(User)
		_ = authStore.AddAction(c.Context(), user.ID, "download", meta.ID, meta.Name, meta.SizeBytes)

		// Browser mode: return attachment to trigger real file download.
		if c.Get("X-Client-Mode") == "browser" {
			raw, err := os.ReadFile(path)
			if err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			c.Attachment(meta.Name)
			return c.Type("application/octet-stream").Send(raw)
		}

		return c.JSON(fiber.Map{"status": "downloaded", "saved_to": path, "file": meta})
	})

	app.Get("/p2p/chunks/:fileID/:idx", func(c fiber.Ctx) error {
		fileID := c.Params("fileID")
		chunkIdx, err := strconv.Atoi(c.Params("idx"))
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid chunk index")
		}
		data, err := storage.ReadChunk(fileID, chunkIdx)
		if err != nil {
			return fiber.NewError(fiber.StatusNotFound, "chunk not found")
		}
		return c.Type("application/octet-stream").Send(data)
	})

	log.Printf("peer %s listening on :%s", cfg.PeerID, cfg.Port)
	return app.Listen(":" + cfg.Port)
}

func authMiddleware(store *AuthStore) fiber.Handler {
	return func(c fiber.Ctx) error {
		token := extractBearerToken(c.Get("Authorization"))
		if token == "" {
			return fiber.NewError(fiber.StatusUnauthorized, "missing bearer token")
		}
		user, err := store.UserByToken(c.Context(), token)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "unauthorized")
		}
		c.Locals("user", user)
		return c.Next()
	}
}

func extractBearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func heartbeatLoop(cfg config.Config, trackerClient *TrackerClient) {
	ticker := time.NewTicker(cfg.HeartbeatPeriod)
	defer ticker.Stop()
	for range ticker.C {
		if err := trackerClient.Heartbeat(context.Background()); err != nil {
			log.Printf("heartbeat failed: %v", err)
		}
	}
}

func buildHTML(peerID string, localFiles []models.FileMetadata, networkFiles []models.FileSummary) string {
	type localView struct {
		ID         string
		Name       string
		SizeBytes  int64
		ChunkCount int
	}
	type networkView struct {
		ID         string
		Name       string
		SizeBytes  int64
		ChunkCount int
		PeersCount int
	}

	local := make([]localView, 0, len(localFiles))
	for _, f := range localFiles {
		local = append(local, localView{
			ID:         f.ID,
			Name:       f.Name,
			SizeBytes:  f.SizeBytes,
			ChunkCount: f.ChunkCount,
		})
	}

	network := make([]networkView, 0, len(networkFiles))
	for _, f := range networkFiles {
		network = append(network, networkView{
			ID:         f.ID,
			Name:       f.Name,
			SizeBytes:  f.SizeBytes,
			ChunkCount: f.ChunkCount,
			PeersCount: f.PeersCount,
		})
	}

	sort.Slice(local, func(i, j int) bool { return local[i].Name < local[j].Name })
	sort.Slice(network, func(i, j int) bool { return network[i].Name < network[j].Name })

	var localRows strings.Builder
	for _, f := range local {
		localRows.WriteString("<tr><td>" + f.Name + "</td><td>" + formatMB(f.SizeBytes) + "</td><td>" + strconv.Itoa(f.ChunkCount) + "</td><td><code>" + f.ID + "</code></td></tr>")
	}
	if localRows.Len() == 0 {
		localRows.WriteString("<tr><td colspan=\"4\" class=\"muted\">Локальных файлов пока нет</td></tr>")
	}

	var networkRows strings.Builder
	for _, f := range network {
		networkRows.WriteString("<tr><td>" + f.Name + "</td><td>" + formatMB(f.SizeBytes) + "</td><td>" + strconv.Itoa(f.ChunkCount) + "</td><td>" + strconv.Itoa(f.PeersCount) + "</td><td><code>" + f.ID + "</code></td><td><button type=\"button\" class=\"download-btn\" data-file-id=\"" + f.ID + "\" data-file-name=\"" + f.Name + "\">Скачать</button></td></tr>")
	}
	if networkRows.Len() == 0 {
		networkRows.WriteString("<tr><td colspan=\"6\" class=\"muted\">В сети пока нет файлов</td></tr>")
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<title>P2P Узел</title>
<style>
body{font-family:Inter,Segoe UI,Arial,sans-serif;background:#0f172a;color:#e2e8f0;margin:0}
.container{max-width:1100px;margin:0 auto;padding:24px}
.card{background:#111b32;border:1px solid #253456;border-radius:14px;padding:16px 18px;margin-bottom:16px;box-shadow:0 6px 20px rgba(0,0,0,.22)}
h1,h2,h3{margin:0 0 12px 0}
.muted{color:#94a3b8}
.top{display:flex;justify-content:space-between;align-items:center;gap:12px;flex-wrap:wrap}
.toolbar{display:flex;gap:8px;flex-wrap:wrap}
button{background:#3b82f6;color:#fff;border:none;border-radius:10px;padding:9px 13px;cursor:pointer}
button.secondary{background:#334155}
button.danger{background:#ef4444}
input{background:#0b1224;border:1px solid #334155;color:#e2e8f0;padding:9px 10px;border-radius:10px;min-width:220px}
table{width:100%%;border-collapse:collapse}
th,td{padding:10px;border-bottom:1px solid #233350;text-align:left}
th{color:#cbd5e1;font-weight:600}
code{font-size:12px;color:#93c5fd}
.hidden{display:none}
#status{display:none;padding:12px;border-radius:10px;margin-bottom:12px}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(280px,1fr));gap:12px}
</style>
</head>
<body>
<div class="container">
  <div class="card">
    <div class="top">
      <div>
        <h1>P2P Файлообмен</h1>
        <div class="muted">ID пира: <b>%s</b></div>
        <div id="auth-user" class="muted" style="margin-top:6px;">Вы не авторизованы</div>
      </div>
      <div class="toolbar">
        <button id="open-auth-btn" class="secondary">Вход и регистрация</button>
        <button id="open-upload-btn">Загрузить файл/видео</button>
        <button id="logout-btn" class="danger">Выйти</button>
      </div>
    </div>
  </div>

  <div id="status"></div>

  <div id="auth-panel" class="card hidden">
    <h2>Регистрация и вход</h2>
    <div class="grid">
      <form id="register-form">
        <h3>Регистрация</h3>
        <input type="text" name="username" placeholder="Логин" required><br><br>
        <input type="password" name="password" placeholder="Пароль (минимум 6)" required><br><br>
        <button type="submit">Создать аккаунт</button>
      </form>
      <form id="login-form">
        <h3>Вход</h3>
        <input type="text" name="username" placeholder="Логин" required><br><br>
        <input type="password" name="password" placeholder="Пароль" required><br><br>
        <button type="submit">Войти</button>
      </form>
    </div>
  </div>

  <div id="upload-panel" class="card hidden">
    <h2>Загрузка</h2>
    <p class="muted">Поддерживаются видео и любые бинарные файлы. Размер отображается в MB.</p>
    <form id="upload-form" enctype="multipart/form-data">
      <input type="file" name="file" accept="video/*,*/*" required>
      <button type="submit">Загрузить</button>
    </form>
  </div>

  <div class="card">
    <h2>Локальные файлы</h2>
    <table>
      <tr><th>Имя</th><th>Размер</th><th>Чанки</th><th>ID файла</th></tr>
      %s
    </table>
  </div>

  <div class="card">
    <h2>Файлы в сети</h2>
    <table>
      <tr><th>Имя</th><th>Размер</th><th>Чанки</th><th>Пиры</th><th>ID файла</th><th>Действие</th></tr>
      %s
    </table>
  </div>

  <div class="card">
    <h2>Моя активность</h2>
    <table id="activity-table">
      <tr><th>Действие</th><th>Файл</th><th>Размер</th><th>Когда</th></tr>
    </table>
  </div>
</div>

<script>
const statusEl=document.getElementById('status');
const userEl=document.getElementById('auth-user');
const authPanel=document.getElementById('auth-panel');
const uploadPanel=document.getElementById('upload-panel');
document.getElementById('open-auth-btn').onclick=()=>authPanel.classList.toggle('hidden');
document.getElementById('open-upload-btn').onclick=()=>uploadPanel.classList.toggle('hidden');
function formatMB(bytes){return (Number(bytes)/(1024*1024)).toFixed(2)+' MB';}
function showStatus(text,ok){
  statusEl.style.display='block';
  statusEl.textContent=text;
  statusEl.style.background=ok?'#062f1f':'#3f1b1b';
  statusEl.style.border=ok?'1px solid #16a34a':'1px solid #ef4444';
  statusEl.style.color=ok?'#bbf7d0':'#fecaca';
}
function token(){return localStorage.getItem('p2p_token')||'';}
function setAuthState(user){
  if(user){
    userEl.textContent='Вы вошли как: '+user.username;
  }else{
    userEl.textContent='Вы не авторизованы';
  }
}
async function authFetch(url,opts={}){
  const headers=Object.assign({},opts.headers||{});
  headers['Authorization']='Bearer '+token();
  const r=await fetch(url,Object.assign({},opts,{headers}));
  if(r.status===401){setAuthState(null);}
  return r;
}
async function loadMe(){
  if(!token()){setAuthState(null);return;}
  const r=await authFetch('/api/v1/auth/me');
  if(!r.ok){setAuthState(null);return;}
  const d=await r.json();
  setAuthState(d.user);
}
async function loadActions(){
  const table=document.getElementById('activity-table');
  table.innerHTML='<tr><th>Действие</th><th>Файл</th><th>Размер</th><th>Когда</th></tr>';
  if(!token()){return;}
  const r=await authFetch('/api/v1/me/actions');
  if(!r.ok){return;}
  const items=await r.json();
  items.forEach((it)=>{
    const tr=document.createElement('tr');
    tr.innerHTML='<td>'+it.action+'</td><td>'+it.file_name+'</td><td>'+formatMB(it.size_bytes)+'</td><td>'+it.created_at+'</td>';
    table.appendChild(tr);
  });
}
document.getElementById('register-form').addEventListener('submit',async(e)=>{
  e.preventDefault();
  const fd=new FormData(e.target);
  const payload={username:String(fd.get('username')).trim(),password:String(fd.get('password'))};
  const r=await fetch('/api/v1/auth/register',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
  if(!r.ok){showStatus('Ошибка регистрации: '+await r.text(),false);return;}
  const d=await r.json();
  localStorage.setItem('p2p_token',d.token);
  showStatus('Регистрация выполнена, вход выполнен',true);
  authPanel.classList.add('hidden');
  await loadMe();await loadActions();
});
document.getElementById('login-form').addEventListener('submit',async(e)=>{
  e.preventDefault();
  const fd=new FormData(e.target);
  const payload={username:String(fd.get('username')).trim(),password:String(fd.get('password'))};
  const r=await fetch('/api/v1/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
  if(!r.ok){showStatus('Ошибка входа: '+await r.text(),false);return;}
  const d=await r.json();
  localStorage.setItem('p2p_token',d.token);
  showStatus('Вход выполнен',true);
  authPanel.classList.add('hidden');
  await loadMe();await loadActions();
});
document.getElementById('logout-btn').addEventListener('click',async()=>{
  if(token()){await authFetch('/api/v1/auth/logout',{method:'POST'});}
  localStorage.removeItem('p2p_token');
  setAuthState(null);
  showStatus('Вы вышли из аккаунта',true);
  await loadActions();
});
const uploadForm=document.getElementById('upload-form');
uploadForm.addEventListener('submit',async(e)=>{
  e.preventDefault();
  if(!token()){showStatus('Нужно войти в аккаунт',false);return;}
  const fd=new FormData(uploadForm);
  showStatus('Загрузка файла...',true);
  try{
    const r=await authFetch('/api/v1/upload',{method:'POST',body:fd});
    if(!r.ok){throw new Error(await r.text());}
    const data=await r.json();
    showStatus('Файл загружен: '+data.file.name+' ('+formatMB(data.file.size_bytes)+')',true);
    uploadPanel.classList.add('hidden');
    await loadActions();
    setTimeout(()=>location.reload(),800);
  }catch(err){
    showStatus('Ошибка загрузки: '+err.message,false);
  }
});
document.querySelectorAll('.download-btn').forEach((btn)=>{
  btn.addEventListener('click',async()=>{
    if(!token()){showStatus('Нужно войти в аккаунт',false);return;}
    const id=btn.dataset.fileId;
    const name=btn.dataset.fileName||'download.bin';
    showStatus('Скачивание '+name+'...',true);
    try{
      const r=await authFetch('/api/v1/download/'+id,{method:'POST',headers:{'X-Client-Mode':'browser'}});
      if(!r.ok){throw new Error(await r.text());}
      const blob=await r.blob();
      const url=URL.createObjectURL(blob);
      const a=document.createElement('a');
      a.href=url;a.download=name;document.body.appendChild(a);a.click();a.remove();URL.revokeObjectURL(url);
      showStatus('Скачивание завершено: '+name,true);
      await loadActions();
      setTimeout(()=>location.reload(),800);
    }catch(err){
      showStatus('Ошибка скачивания: '+err.message,false);
    }
  });
});
loadMe();loadActions();
</script>
</body>
</html>`, peerID, localRows.String(), networkRows.String())
}

func formatMB(sizeBytes int64) string {
	return fmt.Sprintf("%.2f MB", float64(sizeBytes)/(1024*1024))
}
