// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combineReducers} from 'redux';

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

function loading(state = false, action: {type: string}) {
    switch (action.type) {
    case ActionTypes.THREAD_SUMMARY_REQUEST:
        return true;
    case ActionTypes.THREAD_SUMMARY_SUCCESS:
    case ActionTypes.THREAD_SUMMARY_FAILURE:
    case ActionTypes.THREAD_SUMMARY_CLEAR:
        return false;
    default:
        return state;
    }
}

function postId(state: string | null = null, action: {type: string; postId?: string}) {
    switch (action.type) {
    case ActionTypes.THREAD_SUMMARY_REQUEST:
        return action.postId ?? null;
    case ActionTypes.THREAD_SUMMARY_CLEAR:
        return null;
    default:
        return state;
    }
}

function data(state: ThreadSummaryData | null = null, action: {type: string; data?: ThreadSummaryData}) {
    switch (action.type) {
    case ActionTypes.THREAD_SUMMARY_SUCCESS:
        return action.data ?? null;
    case ActionTypes.THREAD_SUMMARY_CLEAR:
    case ActionTypes.THREAD_SUMMARY_REQUEST:
        return null;
    default:
        return state;
    }
}

function error(state: string | null = null, action: {type: string; error?: string}) {
    switch (action.type) {
    case ActionTypes.THREAD_SUMMARY_FAILURE:
        return action.error ?? 'Unknown error';
    case ActionTypes.THREAD_SUMMARY_REQUEST:
    case ActionTypes.THREAD_SUMMARY_CLEAR:
        return null;
    default:
        return state;
    }
}

export default combineReducers({loading, postId, data, error});
