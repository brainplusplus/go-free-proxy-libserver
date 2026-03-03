# Fix Server Shutdown on Ctrl+C Design

**Date:** 2026-03-04
**Status:** Approved

## Problem

Server doesn't stop gracefully when pressing Ctrl+C on Windows. The graceful shutdown code may not properly handle Windows signal delivery.

## Solution

Update `server/main.go` to improve signal handling:

1. Add `os.Interrupt` alongside `syscall.SIGINT` for better Windows compatibility
2. Add a shutdown timeout (5 seconds) to prevent hanging
3. Use `app.ShutdownWithContext()` for timed shutdown
4. Improve shutdown logging for visibility

## Changes

**File:** `server/main.go`

1. Add `"context"` to imports
2. Update signal registration to include `os.Interrupt`
3. Replace `app.Shutdown()` with `app.ShutdownWithContext()` and 5-second timeout

## Code Changes

Replace lines 72-89 with:

```go
// Graceful shutdown with context timeout
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT, os.Interrupt)

go func() {
    log.Printf("Server running on http://localhost:%d", port)
    log.Printf("Swagger UI: http://localhost:%d/swagger", port)
    if err := app.Listen(fmt.Sprintf(":%d", port)); err != nil {
        log.Printf("Server error: %v", err)
    }
}()

<-quit
log.Println("Shutting down server (timeout: 5s)...")

// Create shutdown context with timeout
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := app.ShutdownWithContext(ctx); err != nil {
    log.Printf("Error during shutdown: %v", err)
}
log.Println("Server stopped.")
```

## Success Criteria

- Ctrl+C stops the server within 5 seconds
- Shutdown is logged clearly
- Works on both Windows and Unix systems
