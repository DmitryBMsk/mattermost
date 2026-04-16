// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Client4} from 'mattermost-redux/client';
import {getPost} from 'mattermost-redux/selectors/entities/posts';

import {selectPost} from 'actions/views/rhs';
import {getThreadSummaryPreviousPostId} from 'selectors/views/thread_summary';

import {ActionTypes, RHSStates} from 'utils/constants';

import type {DispatchFunc, GetStateFunc} from 'types/store';

export function showThreadSummary(postId: string) {
    return (dispatch: DispatchFunc) => {
        dispatch({
            type: ActionTypes.UPDATE_RHS_STATE,
            state: RHSStates.THREAD_SUMMARY,
        });

        dispatch(fetchThreadSummary(postId));

        return {data: true};
    };
}

export function backToThreadFromSummary() {
    return (dispatch: DispatchFunc, getState: GetStateFunc) => {
        const postId = getThreadSummaryPreviousPostId(getState());

        dispatch(clearThreadSummary());

        if (postId) {
            const post = getPost(getState(), postId);
            if (post) {
                dispatch(selectPost(post));
                return {data: true};
            }
        }

        dispatch({type: ActionTypes.UPDATE_RHS_STATE, state: null});
        return {data: true};
    };
}

export function fetchThreadSummary(postId: string) {
    return async (dispatch: DispatchFunc) => {
        dispatch({
            type: ActionTypes.THREAD_SUMMARY_REQUEST,
            postId,
            previousPostId: postId,
        });

        try {
            const data = await Client4.postThreadSummary(postId);
            dispatch({
                type: ActionTypes.THREAD_SUMMARY_SUCCESS,
                data,
            });
            return {data};
        } catch (err: unknown) {
            const message = err instanceof Error ? err.message : 'Failed to generate summary';
            dispatch({
                type: ActionTypes.THREAD_SUMMARY_FAILURE,
                error: message,
            });
            return {error: message};
        }
    };
}

export function clearThreadSummary() {
    return {type: ActionTypes.THREAD_SUMMARY_CLEAR};
}
