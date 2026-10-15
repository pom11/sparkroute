// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sqlite

import (
	"context"
	"database/sql"

	"github.com/sparksq/sparkroute/pkg/config/managed"
)

func (s *Store) ReconcileContext(ctx context.Context, owner managed.Owner) (managed.ReconcileContext, error) {
	if err := owner.Validate(); err != nil {
		return managed.ReconcileContext{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return managed.ReconcileContext{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := readCurrent(ctx, tx)
	if err != nil {
		return managed.ReconcileContext{}, err
	}
	sets, err := s.loadSets(ctx, tx)
	if err != nil {
		return managed.ReconcileContext{}, err
	}
	return managed.BuildReconcileContext(sets, owner, current.Version)
}
