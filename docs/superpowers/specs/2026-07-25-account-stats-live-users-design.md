# Account Stats Live Users Refresh Design

## Scope

Improve the account usage statistics dialog without changing its backend API:

- refresh only the "current/recent users" data automatically;
- add independent pagination to the current/recent and date-range user lists;
- align active indicators in a dedicated column and animate active users;
- keep both lists usable on mobile viewports;
- provide a compact manual refresh icon beside the current/recent hint.

The summary cards, charts, model statistics, endpoint statistics, and date-range user list must not be refreshed by the live-user polling loop.

## Data Flow

The dialog continues to perform its existing full load when opened and when the selected date range changes. While it remains open, a five-second polling loop calls `getRecentUsers(accountId)` without date parameters and replaces only `recentUsers`.

The loop runs only while the dialog is open and the document is visible. It skips a tick when a previous live-user request is still running. Closing the dialog stops polling. Polling is silent: existing rows remain visible and only the refresh icon spins. A manual click uses the same guarded refresh function.

Responses are applied only when they still belong to the currently open account, preventing a late response from updating another account's dialog.

## Pagination

Both user lists use independent client-side page state. Their fixed page size comes from `getConfiguredTableDefaultPageSize()` and no page-size selector is displayed.

- Current/recent polling preserves the current page when it remains valid.
- If refreshed data makes that page invalid, it moves to the last valid page.
- Opening the dialog starts both lists at page 1.
- Changing or applying a date range resets only the range-user list to page 1.

The existing shared `Pagination` component provides desktop page numbers and compact mobile previous/page/next controls with `showPageSizeSelector` disabled.

## Presentation

On `sm` and wider viewports, each list remains a table. The current/recent table adds a fixed-width first column for activity. Active rows show a solid green dot with a ping ring; inactive rows leave the cell empty, so all user IDs align. The animation respects reduced-motion preferences.

Below `sm`, each table becomes an unframed, vertically separated list. Identity appears first, followed by a compact grid containing every numeric/time field. The activity indicator retains its own fixed-width position. This avoids horizontal scrolling and nested cards.

The current/recent heading keeps its existing hint. Immediately after the hint, a small icon-only refresh control is rendered without a visible button background or border. It has a tooltip and accessible label, is disabled while a refresh is in flight, and applies `animate-spin` for both manual and automatic refreshes.

## Error Handling

An automatic or manual live-user refresh failure is logged and leaves the currently displayed rows intact. It does not clear the list or replace the whole dialog with a loading/error state. The next interval may retry normally.

Existing full-load error behavior remains unchanged.

## Tests

Component tests use fake timers and mocked account APIs to verify:

- opening the dialog performs the existing full load;
- a five-second tick calls only the no-range recent-users request;
- polling does not reload usage stats or range users;
- hidden or closed dialogs do not poll, and requests do not overlap;
- manual refresh uses the same live-only request and spins the icon;
- both lists paginate independently using the configured default page size;
- page state is clamped after refreshed data shrinks;
- the activity indicator occupies its own column and is absent for inactive users;
- desktop table and mobile list structures are both present.
