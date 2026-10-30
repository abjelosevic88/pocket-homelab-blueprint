<?php

use App\Http\Controllers\PhotoController;
use Illuminate\Support\Facades\Route;

Route::get('/user', [UserController::class, 'show']);
Route::prefix('admin')->group(function () {
    Route::post('/reports', 'ReportController@store');
});
Route::apiResource('photos', PhotoController::class);
$key = env('APP_KEY', 'base64:x');
