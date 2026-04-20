// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ActionTypes} from 'utils/constants';

export interface ThreadSummaryData {
    summary: string;
    key_points: Array<{
        text: string;
        post_ids: string[];
    }>;
    participants: string[];
    thread_post_count: number;
    model: string;
}

export interface ThreadSummaryState {
    loading: boolean;
    postId: string | null;
    data: ThreadSummaryData | null;
    error: string | null;
    previousPostId: string | null;
}

const initialState: ThreadSummaryState = {
    loading: false,
    postId: null,
    data: null,
    error: null,
    previousPostId: null,
};

type ThreadSummaryAction = {
    type: string;
    postId?: string;
    previousPostId?: string;
    data?: ThreadSummaryData;
    error?: string;
};

// Single reducer to ensure success/failure for a stale request are ignored
export default function threadSummary(state = initialState, action: ThreadSummaryAction): ThreadSummaryState {
    switch (action.type) {
    case ActionTypes.THREAD_SUMMARY_REQUEST:
        return {
            ...state,
            loading: true,
            postId: action.postId ?? null,
            data: null,
            error: null,
            previousPostId: action.previousPostId ?? null,
        };
    case ActionTypes.THREAD_SUMMARY_SUCCESS:
        // Ignore stale completion — postId must match current request
        if (action.postId && action.postId !== state.postId) {
            return state;
        }
        return {
            ...state,
            loading: false,
            data: action.data ?? null,
        };
    case ActionTypes.THREAD_SUMMARY_FAILURE:
        if (action.postId && action.postId !== state.postId) {
            return state;
        }
        return {
            ...state,
            loading: false,
            error: action.error ?? 'Unknown error',
        };
    case ActionTypes.THREAD_SUMMARY_CLEAR:
        return initialState;
    default:
        return state;
    }
}
