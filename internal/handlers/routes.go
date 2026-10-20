package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"racket/internal/auth"
)

// Routes returns the app's router. Everything except the health check, the
// landing page, sign in and out, and static assets needs a signed-in user.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	// Reject cross-site state-changing requests (CSRF) using the browser's
	// Sec-Fetch-Site and Origin headers. GET/HEAD/OPTIONS pass through.
	r.Use(http.NewCrossOriginProtection().Handler)
	r.Use(h.auth.Sessions.Middleware)

	r.Get("/healthz", h.Healthz)
	r.Get("/", h.Landing)
	// Sign-in moved into the header; old links to the login page land home.
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
	})
	r.Post("/logout", h.Logout)
	if h.auth.Discord != nil {
		r.Get(discordLoginPath, h.DiscordLogin)
		r.Get(discordCallbackPath, h.DiscordCallback)
	}
	if h.auth.DevLogin {
		r.Post(devLoginPath, h.DevLogin)
	}
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServerFS(h.web.Static())))

	r.Group(func(r chi.Router) {
		r.Use(auth.RequireUser)
		r.Get("/machines", h.MachinesPage)
		r.Get("/machines/new", h.NewMachinePage)
		r.Post("/machines", h.CreateMachine)
		r.Get("/machines/{id}", h.MachineDetailPage)
		r.Post("/machines/{id}/delete", h.DeleteMachine)
		r.Post("/machines/{id}/slots", h.CreateSlot)
		r.Post("/machines/{id}/prices", h.RefreshPrices)
		r.Post("/slots/{id}/part", h.AddPart)
		r.Post("/slots/{id}/part/place", h.PlacePart)
		r.Post("/slots/{id}/part/takeout", h.TakeOutPart)
		r.Post("/slots/{id}/part/delete", h.RemovePart)
		r.Post("/slots/{id}/part/bought", h.MarkPartBought)
		r.Post("/slots/{id}/delete", h.DeleteSlot)
		r.Get("/parts", h.PartsPage)
		r.Post("/parts", h.CreateSparePart)
		r.Post("/parts/import", h.ImportSpares)
		r.Post("/parts/{id}/lend", h.SetPartLendable)
		r.Post("/parts/{id}/return", h.ReturnPart)
		r.Post("/parts/{id}/borrow", h.BorrowPart)
		r.Post("/parts/{id}/delete", h.DeleteSparePart)
		r.Get("/people", h.PeoplePage)
		r.Get("/users/{id}", h.ProfilePage)
		r.Get("/import", h.ImportPage)
		r.Post("/import", h.Import)
	})
	return r
}
