package main

import (
	"autherain/golang_arxiv/internal/observability"
	"net/http"
)

func (app *application) healthcheckHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Start a child span for this specific handler
	ctx, span := observability.StartSpan(ctx, "healthcheckHandler")
	defer span.End()

	// Add events or set attributes as needed
	observability.AddEvent(ctx, "Start giving info")

	env := envelope{
		"status": "available",
		"system_info": map[string]string{
			"environment": app.config.env,
			"version":     version,
			"service":     app.config.serviceName,
		},
	}

	observability.AddEvent(ctx, "Ended giving info")

	app.logger.Info("Healtcheck")

	err := app.writeJSON(w, http.StatusOK, env, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
