package peer

import (
	"context"
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

	app := fiber.New()

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

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"UTF-8\"><title>P2P Peer</title>")
	b.WriteString("<style>body{font-family:Arial,sans-serif;max-width:1000px;margin:20px auto;padding:0 16px;}table{width:100%;border-collapse:collapse;margin:12px 0;}th,td{border:1px solid #ddd;padding:8px;text-align:left;}th{background:#f3f3f3;}code{font-size:12px;}form{margin:0;}button{padding:6px 10px;}</style>")
	b.WriteString("</head><body>")
	b.WriteString("<h1>P2P File Share Peer</h1>")
	b.WriteString("<p>Peer ID: <b>" + peerID + "</b></p>")
	b.WriteString("<p id=\"auth-user\" style=\"font-weight:bold;\"></p>")
	b.WriteString("<div id=\"status\" style=\"display:none;padding:10px;margin:8px 0;border-radius:6px;\"></div>")

	b.WriteString("<h2>Registration and login</h2>")
	b.WriteString("<div style=\"display:flex;gap:16px;flex-wrap:wrap;\">")
	b.WriteString("<form id=\"register-form\" style=\"border:1px solid #ddd;padding:10px;border-radius:8px;\">")
	b.WriteString("<h3>Register</h3>")
	b.WriteString("<input type=\"text\" name=\"username\" placeholder=\"username\" required><br><br>")
	b.WriteString("<input type=\"password\" name=\"password\" placeholder=\"password\" required><br><br>")
	b.WriteString("<button type=\"submit\">Register</button>")
	b.WriteString("</form>")
	b.WriteString("<form id=\"login-form\" style=\"border:1px solid #ddd;padding:10px;border-radius:8px;\">")
	b.WriteString("<h3>Login</h3>")
	b.WriteString("<input type=\"text\" name=\"username\" placeholder=\"username\" required><br><br>")
	b.WriteString("<input type=\"password\" name=\"password\" placeholder=\"password\" required><br><br>")
	b.WriteString("<button type=\"submit\">Login</button>")
	b.WriteString("<button type=\"button\" id=\"logout-btn\" style=\"margin-left:8px;\">Logout</button>")
	b.WriteString("</form>")
	b.WriteString("</div>")

	b.WriteString("<h2>Upload file</h2>")
	b.WriteString("<form id=\"upload-form\" action=\"/api/v1/upload\" method=\"post\" enctype=\"multipart/form-data\">")
	b.WriteString("<input type=\"file\" name=\"file\" required>")
	b.WriteString("<button type=\"submit\">Upload</button>")
	b.WriteString("</form>")

	b.WriteString("<h2>Local files</h2><table><tr><th>Name</th><th>Size</th><th>Chunks</th><th>File ID</th></tr>")
	for _, f := range local {
		b.WriteString("<tr><td>" + f.Name + "</td><td>" + strconv.FormatInt(f.SizeBytes, 10) + "</td><td>" + strconv.Itoa(f.ChunkCount) + "</td><td><code>" + f.ID + "</code></td></tr>")
	}
	b.WriteString("</table>")

	b.WriteString("<h2>Network files</h2><table><tr><th>Name</th><th>Size</th><th>Chunks</th><th>Peers</th><th>File ID</th><th>Action</th></tr>")
	for _, f := range network {
		b.WriteString("<tr><td>" + f.Name + "</td><td>" + strconv.FormatInt(f.SizeBytes, 10) + "</td><td>" + strconv.Itoa(f.ChunkCount) + "</td><td>" + strconv.Itoa(f.PeersCount) + "</td><td><code>" + f.ID + "</code></td>")
		b.WriteString("<td><button type=\"button\" class=\"download-btn\" data-file-id=\"" + f.ID + "\" data-file-name=\"" + f.Name + "\">Download</button></td></tr>")
	}
	b.WriteString("</table>")

	b.WriteString("<p>Tip: after download, file is saved into <code>data/downloads</code> and announced to tracker.</p>")
	b.WriteString("<h2>My activity</h2>")
	b.WriteString("<table id=\"activity-table\"><tr><th>Action</th><th>File</th><th>Size</th><th>When</th></tr></table>")
	b.WriteString("<script>")
	b.WriteString("const statusEl=document.getElementById('status');")
	b.WriteString("const userEl=document.getElementById('auth-user');")
	b.WriteString("function showStatus(text,ok){statusEl.style.display='block';statusEl.textContent=text;statusEl.style.background=ok?'#e8f7ed':'#fdecec';statusEl.style.border=ok?'1px solid #6bbb81':'1px solid #d77';statusEl.style.color=ok?'#1a5f2a':'#8a1f1f';}")
	b.WriteString("function token(){return localStorage.getItem('p2p_token')||'';}")
	b.WriteString("function setAuthState(user){if(user){userEl.textContent='Logged in as: '+user.username;}else{userEl.textContent='Not logged in';}}")
	b.WriteString("async function authFetch(url,opts={}){const headers=Object.assign({},opts.headers||{});headers['Authorization']='Bearer '+token();const r=await fetch(url,Object.assign({},opts,{headers}));if(r.status===401){setAuthState(null);}return r;}")
	b.WriteString("async function loadMe(){const t=token();if(!t){setAuthState(null);return;}const r=await authFetch('/api/v1/auth/me');if(!r.ok){setAuthState(null);return;}const d=await r.json();setAuthState(d.user);}")
	b.WriteString("async function loadActions(){const table=document.getElementById('activity-table');table.innerHTML='<tr><th>Action</th><th>File</th><th>Size</th><th>When</th></tr>';if(!token()){return;}const r=await authFetch('/api/v1/me/actions');if(!r.ok){return;}const items=await r.json();items.forEach((it)=>{const tr=document.createElement('tr');tr.innerHTML='<td>'+it.action+'</td><td>'+it.file_name+'</td><td>'+it.size_bytes+'</td><td>'+it.created_at+'</td>';table.appendChild(tr);});}")
	b.WriteString("document.getElementById('register-form').addEventListener('submit',async(e)=>{e.preventDefault();const fd=new FormData(e.target);const payload={username:String(fd.get('username')).trim(),password:String(fd.get('password'))};const r=await fetch('/api/v1/auth/register',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});if(!r.ok){showStatus('Register failed: '+await r.text(),false);return;}const d=await r.json();localStorage.setItem('p2p_token',d.token);showStatus('Registered and logged in',true);await loadMe();await loadActions();});")
	b.WriteString("document.getElementById('login-form').addEventListener('submit',async(e)=>{e.preventDefault();const fd=new FormData(e.target);const payload={username:String(fd.get('username')).trim(),password:String(fd.get('password'))};const r=await fetch('/api/v1/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});if(!r.ok){showStatus('Login failed: '+await r.text(),false);return;}const d=await r.json();localStorage.setItem('p2p_token',d.token);showStatus('Logged in successfully',true);await loadMe();await loadActions();});")
	b.WriteString("document.getElementById('logout-btn').addEventListener('click',async()=>{if(!token()){return;}await authFetch('/api/v1/auth/logout',{method:'POST'});localStorage.removeItem('p2p_token');showStatus('Logged out',true);await loadMe();await loadActions();});")
	b.WriteString("const uploadForm=document.getElementById('upload-form');")
	b.WriteString("uploadForm.addEventListener('submit',async(e)=>{e.preventDefault();if(!token()){showStatus('Login required',false);return;}const fd=new FormData(uploadForm);showStatus('Uploading...',true);try{const r=await authFetch('/api/v1/upload',{method:'POST',body:fd});if(!r.ok){throw new Error(await r.text());}const data=await r.json();showStatus('File uploaded: '+data.file.name+' (ID '+data.file.id.slice(0,12)+'...)',true);await loadActions();setTimeout(()=>location.reload(),800);}catch(err){showStatus('Upload failed: '+err.message,false);}});")
	b.WriteString("document.querySelectorAll('.download-btn').forEach((btn)=>{btn.addEventListener('click',async()=>{if(!token()){showStatus('Login required',false);return;}const id=btn.dataset.fileId;const name=btn.dataset.fileName||'download.bin';showStatus('Downloading '+name+'...',true);try{const r=await authFetch('/api/v1/download/'+id,{method:'POST',headers:{'X-Client-Mode':'browser'}});if(!r.ok){throw new Error(await r.text());}const blob=await r.blob();const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=name;document.body.appendChild(a);a.click();a.remove();URL.revokeObjectURL(url);showStatus('Download completed: '+name,true);await loadActions();setTimeout(()=>location.reload(),800);}catch(err){showStatus('Download failed: '+err.message,false);}});});")
	b.WriteString("loadMe();loadActions();")
	b.WriteString("</script>")
	b.WriteString("</body></html>")
	return b.String()
}
