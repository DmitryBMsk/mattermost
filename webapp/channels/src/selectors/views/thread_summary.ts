// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {GlobalState} from 'types/store';

export function getThreadSummaryLoading(state: GlobalState): boolean {
    return state.views.threadSummary?.loading ?? false;
}

export function getThreadSummaryData(state: GlobalState) {
    return state.views.threadSummary?.data ?? null;
}

export function getThreadSummaryError(state: GlobalState): string | null {
    return state.views.threadSummary?.error ?? null;
}

export function getThreadSummaryPostId(state: GlobalState): string | null {
    return state.views.threadSummary?.postId ?? null;
}

export function getThreadSummaryPreviousPostId(state: GlobalState): string | null {
    return state.views.threadSummary?.previousPostId ?? null;
}
